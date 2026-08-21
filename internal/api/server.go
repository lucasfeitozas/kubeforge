package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/lucasfeitozas/kubeforge/internal/controller"
	"github.com/lucasfeitozas/kubeforge/internal/k8s"
	"github.com/lucasfeitozas/kubeforge/internal/store"
)

// Server expõe via HTTP o acompanhamento de status e logs de um Componente
// em execução (E4.S4) sobre o cluster resolvido por ClusterProvider — a
// primeira fatia da API descrita em doc.go; CRUD de Componente e as ações
// build/run/cleanup continuam fora do escopo desta história.
type Server struct {
	mux        *http.ServeMux
	components store.ComponentRepository
	clusters   k8s.ClusterProvider
}

// NewServer monta as rotas do Server. components e clusters já devem estar
// prontos para uso (banco migrado, kubeconfig acessível) — NewServer não
// valida nenhum dos dois.
func NewServer(components store.ComponentRepository, clusters k8s.ClusterProvider) *Server {
	s := &Server{
		mux:        http.NewServeMux(),
		components: components,
		clusters:   clusters,
	}
	s.mux.HandleFunc("GET /components/{id}/status", s.handleStatus)
	s.mux.HandleFunc("GET /components/{id}/logs", s.handleLogs)
	return s
}

// ServeHTTP implementa http.Handler, permitindo usar *Server diretamente
// como Handler de um http.Server (ver cmd/kubeforge/main.go).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
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

// writeError mapeia erros de domínio conhecidos (componente/pod não
// encontrado) para 404; qualquer outro erro (targetContext inválido,
// cluster inacessível etc.) vira 500, com o detalhe apenas logado — não
// exposto ao cliente.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrComponentNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, controller.ErrPodNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		slog.Error("erro inesperado no handler da API", "error", err)
		http.Error(w, "erro interno", http.StatusInternalServerError)
	}
}
