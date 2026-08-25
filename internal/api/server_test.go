package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lucasfeitozas/kubeforge/internal/store"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// stubClusterProvider devolve sempre o mesmo clientset, ignorando
// clusterKey — mesmo padrão de internal/controller/runner_test.go.
type stubClusterProvider struct {
	clientset kubernetes.Interface
}

func (s stubClusterProvider) GetClientset(ctx context.Context, clusterKey string) (kubernetes.Interface, error) {
	return s.clientset, nil
}

func newTestServer(t *testing.T, clientset kubernetes.Interface) (*Server, store.ComponentRepository) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	components := store.NewComponentRepository(db)
	server := NewServer(components, stubClusterProvider{clientset: clientset}, store.NewCleanupAuditRepository(db))
	return server, components
}

func newTestComponent(t *testing.T, components store.ComponentRepository) *store.Component {
	t.Helper()
	c := &store.Component{
		Nome:          "componente-de-teste",
		Source:        json.RawMessage(`{"repoUrl":"https://example.com/repo.git","ref":{"type":"branch","value":"main"}}`),
		Resources:     json.RawMessage(`{"requests":{"cpu":"100m"}}`),
		Runtime:       json.RawMessage(`{"workloadKind":"Job"}`),
		TargetContext: json.RawMessage(`{"cluster":"minikube"}`),
	}
	if err := components.Create(context.Background(), c); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return c
}

const validComponentJSON = `{
	"nome": "componente-de-teste",
	"source": {"repoUrl":"https://example.com/repo.git","ref":{"type":"branch","value":"main"}},
	"resources": {"requests":{"cpu":"100m"}},
	"runtime": {"workloadKind":"Job"},
	"targetContext": {"cluster":"minikube"}
}`

func TestHandleCreateComponent_Sucesso(t *testing.T) {
	server, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/components", strings.NewReader(validComponentJSON))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, corpo = %q, esperava 201", rec.Code, rec.Body.String())
	}
	var got componentDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v, corpo = %q", err, rec.Body.String())
	}
	if got.ID == "" {
		t.Errorf("componentDTO.ID vazio, esperava um id gerado")
	}
	if got.Nome != "componente-de-teste" {
		t.Errorf("componentDTO.Nome = %q, esperava %q", got.Nome, "componente-de-teste")
	}
}

func TestHandleCreateComponent_ValidacaoFalha(t *testing.T) {
	server, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/components", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, corpo = %q, esperava 400", rec.Code, rec.Body.String())
	}
	var got validationErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v, corpo = %q", err, rec.Body.String())
	}
	wantFields := map[string]bool{"nome": true, "source": true, "resources": true, "runtime": true, "targetContext": true}
	gotFields := map[string]bool{}
	for _, f := range got.Errors {
		gotFields[f.Field] = true
	}
	for field := range wantFields {
		if !gotFields[field] {
			t.Errorf("validationErrorResponse.Errors = %+v, esperava violação em %q", got.Errors, field)
		}
	}
}

func TestHandleCreateComponent_JSONInvalido(t *testing.T) {
	server, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/components", strings.NewReader(`{`))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, corpo = %q, esperava 400", rec.Code, rec.Body.String())
	}
}

func TestHandleListComponents_Vazio(t *testing.T) {
	server, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/components", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %q, esperava 200", rec.Code, rec.Body.String())
	}
	var got []componentDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v, corpo = %q", err, rec.Body.String())
	}
	if len(got) != 0 {
		t.Fatalf("componentes = %+v, esperava lista vazia", got)
	}
}

func TestHandleListComponents_ComItens(t *testing.T) {
	server, components := newTestServer(t, nil)
	c1 := newTestComponent(t, components)
	c2 := newTestComponent(t, components)

	req := httptest.NewRequest(http.MethodGet, "/components", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %q, esperava 200", rec.Code, rec.Body.String())
	}
	var got []componentDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v, corpo = %q", err, rec.Body.String())
	}
	if len(got) != 2 {
		t.Fatalf("componentes = %+v, esperava 2 itens", got)
	}
	ids := map[string]bool{got[0].ID: true, got[1].ID: true}
	if !ids[c1.ID] || !ids[c2.ID] {
		t.Errorf("ids devolvidos = %v, esperava conter %q e %q", ids, c1.ID, c2.ID)
	}
}

func TestHandleGetComponent_Sucesso(t *testing.T) {
	server, components := newTestServer(t, nil)
	component := newTestComponent(t, components)

	req := httptest.NewRequest(http.MethodGet, "/components/"+component.ID, nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %q, esperava 200", rec.Code, rec.Body.String())
	}
	var got componentDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v, corpo = %q", err, rec.Body.String())
	}
	if got.ID != component.ID || got.Nome != component.Nome {
		t.Errorf("componentDTO = %+v, esperava ID=%q Nome=%q", got, component.ID, component.Nome)
	}
}

func TestHandleGetComponent_NaoEncontrado(t *testing.T) {
	server, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/components/id-inexistente", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, esperava 404", rec.Code)
	}
}

func TestHandleDeleteComponent_Sucesso(t *testing.T) {
	server, components := newTestServer(t, nil)
	component := newTestComponent(t, components)

	req := httptest.NewRequest(http.MethodDelete, "/components/"+component.ID, nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, corpo = %q, esperava 204", rec.Code, rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/components/"+component.ID, nil)
	getRec := httptest.NewRecorder()
	server.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("GET pós-delete status = %d, esperava 404 (componente deveria ter sido removido)", getRec.Code)
	}
}

func TestHandleDeleteComponent_NaoEncontrado(t *testing.T) {
	server, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodDelete, "/components/id-inexistente", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, esperava 404", rec.Code)
	}
}

func newJobPod(name, namespace, jobName string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{batchv1.JobNameLabel: jobName},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func TestHandleStatus_Sucesso(t *testing.T) {
	server, components := newTestServer(t, nil)
	component := newTestComponent(t, components)

	pod := newJobPod(component.ID+"-aaa", "default", component.ID, corev1.PodRunning)
	server.clusters = stubClusterProvider{clientset: fake.NewSimpleClientset(pod)}

	req := httptest.NewRequest(http.MethodGet, "/components/"+component.ID+"/status", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %q, esperava 200", rec.Code, rec.Body.String())
	}
	var got statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v, corpo = %q", err, rec.Body.String())
	}
	if got.Phase != string(corev1.PodRunning) || got.PodName != pod.Name {
		t.Errorf("statusResponse = %+v, esperava Phase=Running PodName=%q", got, pod.Name)
	}
}

func TestHandleStatus_ComponenteNaoEncontrado(t *testing.T) {
	server, _ := newTestServer(t, fake.NewSimpleClientset())

	req := httptest.NewRequest(http.MethodGet, "/components/id-inexistente/status", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, esperava 404", rec.Code)
	}
}

func TestHandleStatus_JobAindaNaoAplicado(t *testing.T) {
	server, components := newTestServer(t, fake.NewSimpleClientset())
	component := newTestComponent(t, components)

	req := httptest.NewRequest(http.MethodGet, "/components/"+component.ID+"/status", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, corpo = %q, esperava 404 (nenhum pod ainda)", rec.Code, rec.Body.String())
	}
}

func TestHandleLogs_NaoFollow(t *testing.T) {
	server, components := newTestServer(t, nil)
	component := newTestComponent(t, components)

	pod := newJobPod(component.ID+"-aaa", "default", component.ID, corev1.PodSucceeded)
	server.clusters = stubClusterProvider{clientset: fake.NewSimpleClientset(pod)}

	req := httptest.NewRequest(http.MethodGet, "/components/"+component.ID+"/logs", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %q, esperava 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "fake logs") {
		t.Errorf("corpo = %q, esperava conter os logs do pod", rec.Body.String())
	}
}

func TestHandleLogs_ComponenteNaoEncontrado(t *testing.T) {
	server, _ := newTestServer(t, fake.NewSimpleClientset())

	req := httptest.NewRequest(http.MethodGet, "/components/id-inexistente/logs", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, esperava 404", rec.Code)
	}
}

func TestHandleLogs_TailLinesInvalido(t *testing.T) {
	server, components := newTestServer(t, fake.NewSimpleClientset())
	component := newTestComponent(t, components)

	req := httptest.NewRequest(http.MethodGet, "/components/"+component.ID+"/logs?tailLines=abc", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, esperava 400", rec.Code)
	}
}

func newManagedJob(name, namespace string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"kubeforge.io/managed": "true", "kubeforge.io/component": name},
		},
	}
}

func TestHandleCleanup_RemoveRecursosRotulados(t *testing.T) {
	job := newManagedJob("componente-a", "default")
	server, _ := newTestServer(t, fake.NewSimpleClientset(job))

	req := httptest.NewRequest(http.MethodPost, "/cleanup", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %q, esperava 200", rec.Code, rec.Body.String())
	}
	var got cleanupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v, corpo = %q", err, rec.Body.String())
	}
	if got.Count != 1 || len(got.Removed) != 1 {
		t.Fatalf("cleanupResponse = %+v, esperava 1 recurso removido", got)
	}
	if got.Removed[0].Kind != "Job" || got.Removed[0].Name != "componente-a" || got.Removed[0].Namespace != "default" {
		t.Errorf("Removed[0] = %+v, esperava Kind=Job Name=componente-a Namespace=default", got.Removed[0])
	}
}

func TestHandleCleanup_NamespacePersonalizado(t *testing.T) {
	job := newManagedJob("componente-b", "kubeforge-workloads")
	server, _ := newTestServer(t, fake.NewSimpleClientset(job))

	req := httptest.NewRequest(http.MethodPost, "/cleanup?namespace=kubeforge-workloads", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %q, esperava 200", rec.Code, rec.Body.String())
	}
	var got cleanupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v, corpo = %q", err, rec.Body.String())
	}
	if got.Count != 1 || got.Removed[0].Namespace != "kubeforge-workloads" {
		t.Fatalf("cleanupResponse = %+v, esperava 1 recurso removido em kubeforge-workloads", got)
	}
}

func TestHandleCleanup_SemRecursosRotuladosDevolveListaVazia(t *testing.T) {
	server, _ := newTestServer(t, fake.NewSimpleClientset())

	req := httptest.NewRequest(http.MethodPost, "/cleanup", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %q, esperava 200", rec.Code, rec.Body.String())
	}
	var got cleanupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v, corpo = %q", err, rec.Body.String())
	}
	if got.Count != 0 || len(got.Removed) != 0 {
		t.Fatalf("cleanupResponse = %+v, esperava lista vazia", got)
	}
}

func TestHandleLogs_Follow_StreamAteHttptestServer(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	components := store.NewComponentRepository(db)
	component := newTestComponent(t, components)

	pod := newJobPod(component.ID+"-aaa", "default", component.ID, corev1.PodRunning)
	clientset := fake.NewSimpleClientset(pod)

	server := NewServer(components, stubClusterProvider{clientset: clientset}, store.NewCleanupAuditRepository(db))
	httpSrv := httptest.NewServer(server)
	t.Cleanup(httpSrv.Close)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(httpSrv.URL + "/components/" + component.ID + "/logs?follow=true")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperava 200", resp.StatusCode)
	}

	buf := make([]byte, len("fake logs"))
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatalf("lendo primeiro trecho do stream: %v", err)
	}
	if string(buf) != "fake logs" {
		t.Errorf("primeiro trecho = %q, esperava %q", buf, "fake logs")
	}

	// Fecha a conexão do lado do cliente: o handler no servidor deve notar
	// o cancelamento do contexto da requisição e encerrar o loop de follow
	// sozinho, sem exigir que o pod fique terminal (ver
	// StreamPodLogs/ctx.Done em internal/controller/status.go).
	resp.Body.Close()
}
