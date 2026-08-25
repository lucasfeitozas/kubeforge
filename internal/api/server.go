package api

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/lucasfeitozas/kubeforge/internal/build"
	"github.com/lucasfeitozas/kubeforge/internal/controller"
	"github.com/lucasfeitozas/kubeforge/internal/k8s"
	"github.com/lucasfeitozas/kubeforge/internal/store"
)

// Server expõe via HTTP o CRUD de Componente (E6.S1), o disparo de build
// (E6.S2) e de run/cleanup por Componente (E6.S3) sob demanda, o
// acompanhamento de status e logs de um Componente em execução (E4.S4), o
// cleanup --all (E5.S2) sobre o cluster resolvido por ClusterProvider, e o
// Console Web estático embutido no binário (E7.S1).
type Server struct {
	mux          *http.ServeMux
	components   store.ComponentRepository
	clusters     k8s.ClusterProvider
	cleanupAudit store.CleanupAuditRepository
	broker       *build.Broker
	runner       *controller.Runner
}

// defaultCleanupNamespace é usado quando POST /cleanup não informa
// ?namespace= — mesmo valor de internal/controller.defaultNamespace, mas
// mantido como constante própria: cleanup --all é namespace-wide, não
// associado a um Componente/targetContext.
const defaultCleanupNamespace = "default"

// NewServer monta as rotas do Server. components, clusters, cleanupAudit,
// broker e runner já devem estar prontos para uso (banco migrado,
// kubeconfig acessível) — NewServer não valida nenhum dos cinco. staticFS
// é o conteúdo de web/static (ver web.StaticFS, E7.S1), servido na raiz.
func NewServer(components store.ComponentRepository, clusters k8s.ClusterProvider, cleanupAudit store.CleanupAuditRepository, broker *build.Broker, runner *controller.Runner, staticFS fs.FS) *Server {
	s := &Server{
		mux:          http.NewServeMux(),
		components:   components,
		clusters:     clusters,
		cleanupAudit: cleanupAudit,
		broker:       broker,
		runner:       runner,
	}
	s.mux.HandleFunc("POST /components", s.handleCreateComponent)
	s.mux.HandleFunc("GET /components", s.handleListComponents)
	s.mux.HandleFunc("GET /components/{id}", s.handleGetComponent)
	s.mux.HandleFunc("DELETE /components/{id}", s.handleDeleteComponent)
	s.mux.HandleFunc("POST /components/{id}/build", s.handleBuildComponent)
	s.mux.HandleFunc("POST /components/{id}/run", s.handleRunComponent)
	s.mux.HandleFunc("POST /components/{id}/cleanup", s.handleCleanupComponent)
	s.mux.HandleFunc("GET /components/{id}/status", s.handleStatus)
	s.mux.HandleFunc("GET /components/{id}/logs", s.handleLogs)
	s.mux.HandleFunc("POST /cleanup", s.handleCleanup)
	s.mux.HandleFunc("GET /openapi.yaml", s.handleOpenAPISpec)
	s.mux.HandleFunc("GET /swagger/{$}", s.handleSwaggerUI)
	// Catch-all (E7.S1): qualquer caminho GET não reconhecido pelas rotas
	// acima cai aqui — o ServeMux do Go 1.22+ já resolve a precedência por
	// especificidade de padrão, sem precisar de nenhuma lógica extra (ver
	// ADR 0018).
	s.mux.Handle("GET /", http.FileServer(http.FS(staticFS)))
	return s
}

// ServeHTTP implementa http.Handler, permitindo usar *Server diretamente
// como Handler de um http.Server (ver cmd/kubeforge/main.go).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// openAPISpecYAML é o contrato OpenAPI 3.0 de todas as rotas deste
// arquivo, embutido no binário via go:embed (fica em internal/api, não em
// docs/, porque go:embed não permite referenciar um caminho fora da árvore
// do pacote). Servido cru em GET /openapi.yaml, e consumido pela página de
// GET /swagger/ (ver ADR 0017).
//
//go:embed openapi.yaml
var openAPISpecYAML string

// handleOpenAPISpec atende GET /openapi.yaml.
func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = io.WriteString(w, openAPISpecYAML)
}

// swaggerUIHTML carrega o bundle do Swagger UI via CDN (unpkg), apontando
// para GET /openapi.yaml — sem dependência Go nova nem asset vendorizado no
// repositório (ver ADR 0017). Só exige internet para abrir a página; os
// demais endpoints da API continuam 100% locais.
const swaggerUIHTML = `<!DOCTYPE html>
<html lang="pt-br">
<head>
  <meta charset="UTF-8">
  <title>KubeForge API — Swagger UI</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-standalone-preset.js"></script>
  <script>
    window.onload = function() {
      window.ui = SwaggerUIBundle({
        url: "/openapi.yaml",
        dom_id: "#swagger-ui",
        presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
      });
    };
  </script>
</body>
</html>
`

// handleSwaggerUI atende GET /swagger/, servindo a UI interativa do
// Swagger (issue #54 — "Configurar swagger nos endpoints").
func (s *Server) handleSwaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, swaggerUIHTML)
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

// handleRunComponent atende POST /components/{id}/run (E6.S3, AC1): rejeita
// com 409 e mensagem clara se o Componente ainda não tiver
// status.phase=Built, senão marca Running de forma síncrona (mesmo padrão
// de handleBuildComponent/ADR 0014: resposta e um GET imediato já refletem
// o novo estado) e dispara controller.Runner.Run em uma goroutine,
// devolvendo 202 sem esperar a execução terminar (ver ADR 0015). Como a
// fase já vira Running antes da resposta, uma segunda chamada de run
// enquanto a primeira ainda está em andamento cai na mesma checagem de
// fase — sem precisar de lock dedicado.
func (s *Server) handleRunComponent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	component, err := s.components.Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}

	if component.Phase != store.PhaseBuilt {
		http.Error(w, fmt.Sprintf(
			"componente %q não pode ser executado: phase atual é %q, esperado %q (build ainda não concluído com sucesso)",
			id, component.Phase, store.PhaseBuilt,
		), http.StatusConflict)
		return
	}

	if err := s.components.UpdateBuildStatus(r.Context(), component.ID, store.PhaseRunning, component.BuildImageDigest, ""); err != nil {
		writeError(w, err)
		return
	}

	// context.Background(), não r.Context(): mesma razão de
	// handleBuildComponent — a execução precisa continuar mesmo depois da
	// resposta HTTP ser enviada.
	go func() {
		if err := s.runner.Run(context.Background(), component); err != nil {
			slog.Error("execução assíncrona falhou", "component_id", component.ID, "error", err)
		}
	}()

	writeJSON(w, http.StatusAccepted, buildTriggerResponse{ComponentID: component.ID, Phase: store.PhaseRunning})
}

// handleCleanupComponent atende POST /components/{id}/cleanup (E6.S3, AC2):
// remove os recursos (Job principal, Job de verificação, Pods, PVC) do
// Componente id via controller.RunComponentCleanup — que, por os nomes
// desses recursos serem determinísticos por componentID, sempre corresponde
// à execução mais recente (ver ADR 0015). Responde com os mesmos tipos já
// usados por handleCleanup (POST /cleanup global).
func (s *Server) handleCleanupComponent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	results, err := controller.RunComponentCleanup(r.Context(), s.clusters, s.components, id)

	if auditErr := controller.PersistCleanupAudit(r.Context(), s.cleanupAudit, results); auditErr != nil {
		slog.Error("erro ao registrar auditoria de cleanup do componente", "component_id", id, "error", auditErr)
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
// follow=false (default) devolve o snapshot atual dos logs em texto plano e
// encerra a resposta — o fallback estático pedido quando o client não
// suporta streaming (E6.S4, AC2). follow=true transmite os logs via
// Server-Sent Events (text/event-stream), reenviando novos eventos
// conforme controller.StreamPodLogs os produz (E6.S4, AC1; ver ADR 0016 —
// supera a rejeição de SSE da ADR 0008).
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	follow := r.URL.Query().Get("follow") == "true"
	tailLines, err := parseTailLines(r.URL.Query().Get("tailLines"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")

	tw := &trackingWriter{w: w}
	if follow {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		tw.w = &sseWriter{w: w}
		if flusher, ok := w.(http.Flusher); ok {
			tw.flusher = flusher
		}
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
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

// sseWriter adapta os bytes de log crus escritos por
// controller.StreamPodLogs (um io.Writer comum, sem noção de HTTP) para o
// framing text/event-stream (E6.S4, AC1; ver ADR 0016). Cada chamada de
// Write vira um evento SSE completo e imediato — uma linha "data: " por
// linha de log contida no chunk, seguida de uma linha em branco — sem reter
// nada entre chamadas. Isso importa na prática: o fixture "fake logs" do
// fake clientset (usado nos testes) nunca termina em "\n"; um framing que
// esperasse um "\n" para fechar o evento travaria para sempre.
type sseWriter struct {
	w io.Writer
}

func (s *sseWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for _, line := range bytes.Split(bytes.TrimRight(p, "\n"), []byte("\n")) {
		if _, err := fmt.Fprintf(s.w, "data: %s\n", line); err != nil {
			return 0, err
		}
	}
	_, err := io.WriteString(s.w, "\n")
	return len(p), err
}

// trackingWriter registra se algum byte já foi efetivamente escrito na
// resposta HTTP, para que handleLogs só tente reportar um status de erro
// enquanto isso ainda for possível (nenhum Write anterior). Em follow=true
// também aciona http.Flusher a cada escrita, para que o cliente receba cada
// trecho de log (ou evento SSE) assim que produzido, sem esperar o buffer
// HTTP encher. w é io.Writer, não http.ResponseWriter: em follow=true
// envolve um *sseWriter em vez do ResponseWriter cru.
type trackingWriter struct {
	w       io.Writer
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
