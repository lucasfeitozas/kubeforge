package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/lucasfeitozas/kubeforge/internal/build"
	"github.com/lucasfeitozas/kubeforge/internal/controller"
	"github.com/lucasfeitozas/kubeforge/internal/k8s"
	"github.com/lucasfeitozas/kubeforge/internal/store"
)

// Server expõe via HTTP o CRUD de Componente (E6.S1), o disparo de build sob
// demanda (E6.S2), o acompanhamento de status e logs de um Componente em
// execução (E4.S4), e o cleanup --all (E5.S2) sobre o cluster resolvido por
// ClusterProvider — a ação run sob demanda (E6.S3) continua fora do escopo
// desta história.
type Server struct {
	mux          *http.ServeMux
	components   store.ComponentRepository
	clusters     k8s.ClusterProvider
	cleanupAudit store.CleanupAuditRepository
	broker       *build.Broker
}

// defaultCleanupNamespace é usado quando POST /cleanup não informa
// ?namespace= — mesmo valor de internal/controller.defaultNamespace, mas
// mantido como constante própria: cleanup --all é namespace-wide, não
// associado a um Componente/targetContext.
const defaultCleanupNamespace = "default"

// NewServer monta as rotas do Server. components, clusters, cleanupAudit e
// broker já devem estar prontos para uso (banco migrado, kubeconfig
// acessível) — NewServer não valida nenhum dos quatro.
func NewServer(components store.ComponentRepository, clusters k8s.ClusterProvider, cleanupAudit store.CleanupAuditRepository, broker *build.Broker) *Server {
	s := &Server{
		mux:          http.NewServeMux(),
		components:   components,
		clusters:     clusters,
		cleanupAudit: cleanupAudit,
		broker:       broker,
	}
	s.mux.HandleFunc("POST /components", s.handleCreateComponent)
	s.mux.HandleFunc("GET /components", s.handleListComponents)
	s.mux.HandleFunc("GET /components/{id}", s.handleGetComponent)
	s.mux.HandleFunc("DELETE /components/{id}", s.handleDeleteComponent)
	s.mux.HandleFunc("POST /components/{id}/build", s.handleBuildComponent)
	s.mux.HandleFunc("GET /components/{id}/status", s.handleStatus)
	s.mux.HandleFunc("GET /components/{id}/logs", s.handleLogs)
	s.mux.HandleFunc("POST /cleanup", s.handleCleanup)
	return s
}

// ServeHTTP implementa http.Handler, permitindo usar *Server diretamente
// como Handler de um http.Server (ver cmd/kubeforge/main.go).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// componentDTO é o corpo JSON de requisição/resposta do CRUD de Componente
// (POST/GET/DELETE /components), espelhando o schema "Componente" da seção
// 2.2 de docs/ARCHITECTURE.md, mais o objeto "status" (mesmo formato do
// status.* do CRD, docs/ARCHITECTURE.md linhas 169-171) — presente só nas
// respostas, ignorado se enviado em POST /components (ver ADR 0014, que
// supera a exclusão original da ADR 0013: não há outro canal para observar
// o progresso de um build disparado por POST /components/{id}/build).
type componentDTO struct {
	ID            string              `json:"id,omitempty"`
	Nome          string              `json:"nome"`
	Descricao     string              `json:"descricao,omitempty"`
	Source        json.RawMessage     `json:"source,omitempty"`
	Build         json.RawMessage     `json:"build,omitempty"`
	Resources     json.RawMessage     `json:"resources,omitempty"`
	Runtime       json.RawMessage     `json:"runtime,omitempty"`
	Hooks         json.RawMessage     `json:"hooks,omitempty"`
	TargetContext json.RawMessage     `json:"targetContext,omitempty"`
	Lifecycle     json.RawMessage     `json:"lifecycle,omitempty"`
	Status        *componentStatusDTO `json:"status,omitempty"`
}

// componentStatusDTO reflete status.phase/buildImageDigest/errorMessage do
// Componente (E6.S2, AC2: "endpoint de consulta de status reflete o
// progresso").
type componentStatusDTO struct {
	Phase            string `json:"phase"`
	BuildImageDigest string `json:"buildImageDigest,omitempty"`
	ErrorMessage     string `json:"errorMessage,omitempty"`
}

func componentToDTO(c *store.Component) componentDTO {
	return componentDTO{
		ID:            c.ID,
		Nome:          c.Nome,
		Descricao:     c.Descricao,
		Source:        c.Source,
		Build:         c.Build,
		Resources:     c.Resources,
		Runtime:       c.Runtime,
		Hooks:         c.Hooks,
		TargetContext: c.TargetContext,
		Lifecycle:     c.Lifecycle,
		Status: &componentStatusDTO{
			Phase:            c.Phase,
			BuildImageDigest: c.BuildImageDigest,
			ErrorMessage:     c.ErrorMessage,
		},
	}
}

// toComponent converte o DTO recebido no corpo de POST /components para
// store.Component; ID é ignorado (o repository o gera em Create).
func (dto componentDTO) toComponent() *store.Component {
	return &store.Component{
		Nome:          dto.Nome,
		Descricao:     dto.Descricao,
		Source:        dto.Source,
		Build:         dto.Build,
		Resources:     dto.Resources,
		Runtime:       dto.Runtime,
		Hooks:         dto.Hooks,
		TargetContext: dto.TargetContext,
		Lifecycle:     dto.Lifecycle,
	}
}

// handleCreateComponent atende POST /components (E6.S1, AC1). O corpo é
// decodificado e delegado a store.Component.Validate (via
// ComponentRepository.Create); falhas de validação viram 400 através de
// writeError/*store.ValidationError.
func (s *Server) handleCreateComponent(w http.ResponseWriter, r *http.Request) {
	var dto componentDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		http.Error(w, fmt.Sprintf("corpo da requisição inválido: %s", err), http.StatusBadRequest)
		return
	}

	c := dto.toComponent()
	if err := s.components.Create(r.Context(), c); err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, componentToDTO(c))
}

// handleListComponents atende GET /components, devolvendo um array JSON
// (nunca null, mesmo vazio — ver store.ComponentRepository.List).
func (s *Server) handleListComponents(w http.ResponseWriter, r *http.Request) {
	components, err := s.components.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}

	dtos := make([]componentDTO, len(components))
	for i, c := range components {
		dtos[i] = componentToDTO(c)
	}
	writeJSON(w, http.StatusOK, dtos)
}

// handleGetComponent atende GET /components/{id}.
func (s *Server) handleGetComponent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	c, err := s.components.Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, componentToDTO(c))
}

// handleDeleteComponent atende DELETE /components/{id}. Se houver execuções
// referenciando o componente (ON DELETE RESTRICT), o erro de driver SQL cru
// cai no case default de writeError (500) — limitação aceita, sem
// precedente hoje no projeto para mapear FK-restrict a 409 (ver ADR 0013).
func (s *Server) handleDeleteComponent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := s.components.Delete(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// buildTriggerResponse é o corpo JSON devolvido por POST
// /components/{id}/build.
type buildTriggerResponse struct {
	ComponentID string `json:"componentId"`
	Phase       string `json:"phase"`
}

// handleBuildComponent atende POST /components/{id}/build (E6.S2, AC1):
// marca o Componente como Building e dispara build.Broker.Run em uma
// goroutine, devolvendo 202 imediatamente sem esperar o build terminar. O
// diretório de clone é criado aqui (Broker.Run exige que já exista e esteja
// vazio) e removido ao final da goroutine; falhas do build em si (source
// inválido, Dockerfile ausente, docker build com erro etc.) são persistidas
// pelo próprio Broker como status.phase=Failed, observável via
// GET /components/{id} (ver ADR 0014).
func (s *Server) handleBuildComponent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	component, err := s.components.Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}

	cloneDir, err := os.MkdirTemp("", "kubeforge-build-"+component.ID+"-")
	if err != nil {
		slog.Error("erro ao criar diretório temporário de build", "component_id", component.ID, "error", err)
		http.Error(w, "erro interno", http.StatusInternalServerError)
		return
	}

	if err := s.components.UpdateBuildStatus(r.Context(), component.ID, store.PhaseBuilding, "", ""); err != nil {
		os.RemoveAll(cloneDir)
		writeError(w, err)
		return
	}

	// context.Background(), não r.Context(): o build precisa continuar
	// mesmo depois da resposta HTTP ser enviada e a requisição encerrada.
	go func() {
		defer os.RemoveAll(cloneDir)
		if err := s.broker.Run(context.Background(), component, cloneDir, false); err != nil {
			slog.Error("build assíncrona falhou", "component_id", component.ID, "error", err)
		}
	}()

	writeJSON(w, http.StatusAccepted, buildTriggerResponse{ComponentID: component.ID, Phase: store.PhaseBuilding})
}

// statusResponse é o corpo JSON devolvido por GET /components/{id}/status.
type statusResponse struct {
	PodName string `json:"podName"`
	Phase   string `json:"phase"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	status, err := controller.GetPodStatus(r.Context(), s.clusters, s.components, id)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{
		PodName: status.PodName,
		Phase:   status.Phase,
		Reason:  status.Reason,
		Message: status.Message,
	})
}

// handleLogs atende GET /components/{id}/logs?follow=true|false&tailLines=N.
// follow=false (default) devolve o snapshot atual dos logs e encerra a
// resposta; follow=true mantém a conexão HTTP aberta (chunked, sem
// Content-Length) reenviando novas linhas conforme controller.StreamPodLogs
// as produz — equivalente a `kubectl logs -f`, mas via HTTP simples,
// consumível com curl (ver docs/adrs/0008-status-e-logs-http-poll.md).
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	follow := r.URL.Query().Get("follow") == "true"
	tailLines, err := parseTailLines(r.URL.Query().Get("tailLines"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	tw := &trackingWriter{w: w}
	if follow {
		if flusher, ok := w.(http.Flusher); ok {
			tw.flusher = flusher
		}
	}

	if err := controller.StreamPodLogs(r.Context(), s.clusters, s.components, id, tw, follow, tailLines, 0); err != nil {
		if !tw.wrote {
			writeError(w, err)
			return
		}
		// A resposta já começou a ser escrita: o header/status HTTP não pode
		// mais ser alterado nesse ponto, então só registramos o erro.
		slog.Error("erro ao transmitir logs do componente", "component_id", id, "error", err)
	}
}

// cleanupResponse é o corpo JSON devolvido por POST /cleanup.
type cleanupResponse struct {
	Removed []cleanupItem `json:"removed"`
	Count   int           `json:"count"`
}

type cleanupItem struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// handleCleanup atende POST /cleanup?namespace=N (default
// defaultCleanupNamespace), removendo todo Job, Pod e PersistentVolumeClaim
// rotulado kubeforge.io/managed=true no namespace (E5.S2, AC1/AC2) e
// registrando cada remoção no log de auditoria (AC3). Um erro ao registrar
// a auditoria é só logado — não deve mascarar o resultado da limpeza em si,
// que já aconteceu no cluster.
func (s *Server) handleCleanup(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = defaultCleanupNamespace
	}

	results, err := controller.RunCleanup(r.Context(), s.clusters, k8s.MinikubeClusterKey, namespace)

	if auditErr := controller.PersistCleanupAudit(r.Context(), s.cleanupAudit, results); auditErr != nil {
		slog.Error("erro ao registrar auditoria de cleanup", "namespace", namespace, "error", auditErr)
	}

	if err != nil {
		writeError(w, err)
		return
	}

	items := make([]cleanupItem, len(results))
	for i, res := range results {
		items[i] = cleanupItem{Kind: res.Kind, Name: res.Name, Namespace: res.Namespace}
	}
	writeJSON(w, http.StatusOK, cleanupResponse{Removed: items, Count: len(items)})
}

// trackingWriter registra se algum byte já foi efetivamente escrito na
// resposta HTTP, para que handleLogs só tente reportar um status de erro
// enquanto isso ainda for possível (nenhum Write anterior). Em follow=true
// também aciona http.Flusher a cada escrita, para que o cliente receba cada
// trecho de log assim que produzido, sem esperar o buffer HTTP encher.
type trackingWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	wrote   bool
}

func (t *trackingWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		t.wrote = true
	}
	n, err := t.w.Write(p)
	if t.flusher != nil {
		t.flusher.Flush()
	}
	return n, err
}

func parseTailLines(raw string) (*int64, error) {
	if raw == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("parâmetro tailLines inválido: %q", raw)
	}
	return &n, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("erro ao serializar resposta JSON", "error", err)
	}
}

// validationErrorResponse é o corpo JSON devolvido quando POST /components
// falha a validação de store.Component.Validate (400), listando todos os
// campos inválidos de uma vez (fail-slow, não fail-fast).
type validationErrorResponse struct {
	Errors []fieldErrorDTO `json:"errors"`
}

type fieldErrorDTO struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// writeError mapeia erros de domínio conhecidos para o status HTTP
// correspondente: violação de validação de Componente vira 400 com o
// detalhe por campo, componente/pod não encontrado vira 404; qualquer outro
// erro (targetContext inválido, cluster inacessível etc.) vira 500, com o
// detalhe apenas logado — não exposto ao cliente.
func writeError(w http.ResponseWriter, err error) {
	var verr *store.ValidationError
	switch {
	case errors.As(err, &verr):
		fields := make([]fieldErrorDTO, len(verr.Fields))
		for i, f := range verr.Fields {
			fields[i] = fieldErrorDTO{Field: f.Field, Message: f.Message}
		}
		writeJSON(w, http.StatusBadRequest, validationErrorResponse{Errors: fields})
	case errors.Is(err, store.ErrComponentNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, controller.ErrPodNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		slog.Error("erro inesperado no handler da API", "error", err)
		http.Error(w, "erro interno", http.StatusInternalServerError)
	}
}
