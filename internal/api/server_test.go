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
