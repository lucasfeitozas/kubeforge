package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lucasfeitozas/kubeforge/internal/build"
	"github.com/lucasfeitozas/kubeforge/internal/controller"
	"github.com/lucasfeitozas/kubeforge/internal/store"
	"github.com/lucasfeitozas/kubeforge/web"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
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
	executions := store.NewExecutionRepository(db)
	broker := &build.Broker{
		Cloner:     build.NewGitCloner(),
		Builder:    build.NewDockerBuilder(),
		Components: components,
		Executions: executions,
	}
	runner := &controller.Runner{
		ClusterProvider: stubClusterProvider{clientset: clientset},
		Components:      components,
		Executions:      executions,
	}
	server := NewServer(components, stubClusterProvider{clientset: clientset}, store.NewCleanupAuditRepository(db), broker, runner, web.StaticFS())
	return server, components
}

// newJobCompleteReactor injeta JobCondition{Type: JobComplete,
// Status: True} no objeto devolvido por Create antes do fake tracker
// armazená-lo, tornando testes de controller.Runner.Run determinísticos sem
// sleep — mesma técnica de internal/controller/runner_test.go
// (newJobConditionReactor, não reaproveitável entre pacotes/arquivos
// _test.go).
func newJobCompleteReactor() ktesting.ReactionFunc {
	return func(action ktesting.Action) (bool, k8sruntime.Object, error) {
		createAction, ok := action.(ktesting.CreateAction)
		if !ok {
			return false, nil, nil
		}
		job, ok := createAction.GetObject().(*batchv1.Job)
		if !ok {
			return false, nil, nil
		}
		job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
		return false, nil, nil
	}
}

// stubBuildCloner e stubBuildBuilder são Cloner/Builder fakes controlados
// diretamente pelo teste, para injetar em server.broker sem depender de git
// ou docker reais — mesma convenção de stubCloner/stubBuilder em
// internal/build/broker_test.go (não reaproveitáveis diretamente por
// estarem em outro pacote/arquivo _test.go).
type stubBuildCloner struct {
	result *build.CloneResult
	err    error
}

func (s *stubBuildCloner) Clone(ctx context.Context, spec build.CloneSpec, destDir string) (*build.CloneResult, error) {
	return s.result, s.err
}

type stubBuildBuilder struct {
	result *build.BuildResult
	err    error
}

func (s *stubBuildBuilder) Build(ctx context.Context, spec build.BuildSpec) (*build.BuildResult, error) {
	return s.result, s.err
}

// waitForComponentPhase faz polling em components.Get até que o componente
// id atinja phase want ou timeout se esgote — necessário porque
// handleBuildComponent dispara o build em uma goroutine (E6.S2, AC1): o
// teste não tem outro sinal síncrono de que o Broker.Run terminou.
func waitForComponentPhase(t *testing.T, components store.ComponentRepository, id, want string, timeout time.Duration) *store.Component {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		c, err := components.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if c.Phase == want {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("componente %q não atingiu phase=%q a tempo (phase atual = %q)", id, want, c.Phase)
		}
		time.Sleep(5 * time.Millisecond)
	}
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

func TestHandleBuildComponent_Sucesso(t *testing.T) {
	server, components := newTestServer(t, nil)
	component := newTestComponent(t, components)

	server.broker = &build.Broker{
		Cloner:     &stubBuildCloner{result: &build.CloneResult{Dir: t.TempDir(), CommitSHA: "abc1234"}},
		Builder:    &stubBuildBuilder{result: &build.BuildResult{ImageTag: "kubeforge/teste:abc1234", CommitSHA: "abc1234", Digest: "sha256:teste"}},
		Components: components,
		Executions: server.broker.Executions,
	}

	req := httptest.NewRequest(http.MethodPost, "/components/"+component.ID+"/build", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, corpo = %q, esperava 202", rec.Code, rec.Body.String())
	}
	var got buildTriggerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v, corpo = %q", err, rec.Body.String())
	}
	if got.ComponentID != component.ID || got.Phase != store.PhaseBuilding {
		t.Errorf("buildTriggerResponse = %+v, esperava ComponentID=%q Phase=%q", got, component.ID, store.PhaseBuilding)
	}

	updated := waitForComponentPhase(t, components, component.ID, store.PhaseBuilt, 2*time.Second)
	if updated.BuildImageDigest != "sha256:teste" {
		t.Errorf("BuildImageDigest = %q, esperava %q", updated.BuildImageDigest, "sha256:teste")
	}
}

func TestHandleBuildComponent_ComponenteNaoEncontrado(t *testing.T) {
	server, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/components/id-inexistente/build", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, esperava 404", rec.Code)
	}
}

func TestHandleBuildComponent_FalhaAssincronaMarcaComponenteFailed(t *testing.T) {
	server, components := newTestServer(t, nil)
	component := newTestComponent(t, components)

	server.broker = &build.Broker{
		Cloner:     &stubBuildCloner{err: errors.New("clone falhou: repositório inacessível")},
		Builder:    &stubBuildBuilder{},
		Components: components,
		Executions: server.broker.Executions,
	}

	req := httptest.NewRequest(http.MethodPost, "/components/"+component.ID+"/build", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, corpo = %q, esperava 202 mesmo quando o build falha de forma assíncrona", rec.Code, rec.Body.String())
	}

	updated := waitForComponentPhase(t, components, component.ID, store.PhaseFailed, 2*time.Second)
	if updated.ErrorMessage == "" {
		t.Errorf("ErrorMessage vazio, esperava a causa da falha de clone")
	}
}

func TestHandleRunComponent_Sucesso(t *testing.T) {
	server, components := newTestServer(t, nil)
	component := newTestComponent(t, components)
	if err := components.UpdateBuildStatus(context.Background(), component.ID, store.PhaseBuilt, "sha256:teste", ""); err != nil {
		t.Fatalf("UpdateBuildStatus() error = %v", err)
	}
	component.Phase = store.PhaseBuilt
	component.BuildImageDigest = "sha256:teste"

	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("create", "jobs", newJobCompleteReactor())
	server.clusters = stubClusterProvider{clientset: clientset}
	server.runner = &controller.Runner{
		ClusterProvider: server.clusters,
		Components:      components,
		Executions:      server.broker.Executions,
		PollInterval:    time.Millisecond,
	}

	req := httptest.NewRequest(http.MethodPost, "/components/"+component.ID+"/run", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, corpo = %q, esperava 202", rec.Code, rec.Body.String())
	}
	var got buildTriggerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v, corpo = %q", err, rec.Body.String())
	}
	if got.ComponentID != component.ID || got.Phase != store.PhaseRunning {
		t.Errorf("buildTriggerResponse = %+v, esperava ComponentID=%q Phase=%q", got, component.ID, store.PhaseRunning)
	}

	waitForComponentPhase(t, components, component.ID, store.PhaseSucceeded, 2*time.Second)
}

func TestHandleRunComponent_PhaseNaoBuilt(t *testing.T) {
	server, components := newTestServer(t, nil)
	component := newTestComponent(t, components)

	req := httptest.NewRequest(http.MethodPost, "/components/"+component.ID+"/run", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, corpo = %q, esperava 409", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), store.PhaseBuilt) {
		t.Errorf("corpo = %q, esperava mensagem clara mencionando %q", rec.Body.String(), store.PhaseBuilt)
	}
}

func TestHandleRunComponent_ComponenteNaoEncontrado(t *testing.T) {
	server, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/components/id-inexistente/run", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, esperava 404", rec.Code)
	}
}

func TestHandleCleanupComponent_RemoveApenasRecursosDoComponente(t *testing.T) {
	server, components := newTestServer(t, nil)
	component := newTestComponent(t, components)

	componentJob := newManagedJob(component.ID, "default")
	outroJob := newManagedJob("outro-componente", "default")
	server.clusters = stubClusterProvider{clientset: fake.NewSimpleClientset(componentJob, outroJob)}

	req := httptest.NewRequest(http.MethodPost, "/components/"+component.ID+"/cleanup", nil)
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
		t.Fatalf("cleanupResponse = %+v, esperava 1 recurso removido (só o do componente alvo)", got)
	}
	if got.Removed[0].Name != component.ID {
		t.Errorf("Removed[0].Name = %q, esperava %q", got.Removed[0].Name, component.ID)
	}
}

func TestHandleCleanupComponent_ComponenteNaoEncontrado(t *testing.T) {
	server, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/components/id-inexistente/cleanup", nil)
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

func TestSSEWriter_Write(t *testing.T) {
	tests := []struct {
		name  string
		chunk string
		want  string
	}{
		{
			name:  "chunk sem newline final (fixture fake logs do fake clientset)",
			chunk: "fake logs",
			want:  "data: fake logs\n\n",
		},
		{
			name:  "chunk multi-linha",
			chunk: "linha1\nlinha2\n",
			want:  "data: linha1\ndata: linha2\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			w := &sseWriter{w: &buf}

			n, err := w.Write([]byte(tt.chunk))
			if err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			if n != len(tt.chunk) {
				t.Errorf("n = %d, esperava %d", n, len(tt.chunk))
			}
			if buf.String() != tt.want {
				t.Errorf("saída = %q, esperava %q", buf.String(), tt.want)
			}
		})
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
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, esperava texto plano (fallback estático, E6.S4 AC2)", ct)
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

	executions := store.NewExecutionRepository(db)
	broker := &build.Broker{
		Cloner:     build.NewGitCloner(),
		Builder:    build.NewDockerBuilder(),
		Components: components,
		Executions: executions,
	}
	runner := &controller.Runner{
		ClusterProvider: stubClusterProvider{clientset: clientset},
		Components:      components,
		Executions:      executions,
	}
	server := NewServer(components, stubClusterProvider{clientset: clientset}, store.NewCleanupAuditRepository(db), broker, runner, web.StaticFS())
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
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, esperava text/event-stream (E6.S4, AC1)", ct)
	}

	const wantEvent = "data: fake logs\n\n"
	buf := make([]byte, len(wantEvent))
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("lendo primeiro evento SSE do stream: %v", err)
	}
	if string(buf) != wantEvent {
		t.Errorf("primeiro evento = %q, esperava %q", buf, wantEvent)
	}

	// Fecha a conexão do lado do cliente: o handler no servidor deve notar
	// o cancelamento do contexto da requisição e encerrar o loop de follow
	// sozinho, sem exigir que o pod fique terminal (ver
	// StreamPodLogs/ctx.Done em internal/controller/status.go).
	resp.Body.Close()
}

func TestHandleOpenAPISpec_Sucesso(t *testing.T) {
	server, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperava 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/yaml; charset=utf-8" {
		t.Errorf("Content-Type = %q, esperava application/yaml; charset=utf-8", ct)
	}
	if !strings.Contains(rec.Body.String(), "openapi: 3.0.3") {
		t.Errorf("corpo não parece um documento OpenAPI: %q", rec.Body.String()[:min(80, rec.Body.Len())])
	}
	if !strings.Contains(rec.Body.String(), "/components/{id}/build") {
		t.Errorf("corpo não documenta /components/{id}/build")
	}
}

func TestHandleSwaggerUI_Sucesso(t *testing.T) {
	server, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/swagger/", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperava 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, esperava text/html; charset=utf-8", ct)
	}
	if !strings.Contains(rec.Body.String(), `url: "/openapi.yaml"`) {
		t.Errorf("corpo não aponta o Swagger UI para /openapi.yaml: %q", rec.Body.String())
	}
}

func TestHandleStaticAssets_ServeIndexHTML(t *testing.T) {
	server, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %q, esperava 200", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, esperava começar com text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "KubeForge") {
		t.Errorf("corpo não parece o index.html do Console: %q", rec.Body.String())
	}
}

func TestHandleStaticAssets_404ParaCaminhoInexistente(t *testing.T) {
	server, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/nao-existe.js", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, esperava 404", rec.Code)
	}
}
