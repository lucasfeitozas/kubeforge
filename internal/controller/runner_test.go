package controller

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lucasfeitozas/kubeforge/internal/store"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// stubClusterProvider devolve sempre o mesmo clientset (tipicamente um
// fake.NewSimpleClientset()), ignorando clusterKey — o valor de
// targetContext.cluster no fixture de teste só existe para satisfazer
// store.Component.Validate() (que exige "minikube"|"eks"), não para
// roteamento real.
type stubClusterProvider struct {
	clientset kubernetes.Interface
	err       error
}

func (s stubClusterProvider) GetClientset(ctx context.Context, clusterKey string) (kubernetes.Interface, error) {
	return s.clientset, s.err
}

func newRunnerTestDB(t *testing.T) (store.ComponentRepository, store.ExecutionRepository) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return store.NewComponentRepository(db), store.NewExecutionRepository(db)
}

func newRunnerTestComponent(t *testing.T, components store.ComponentRepository, hooksJSON, targetContextJSON string) *store.Component {
	t.Helper()
	ctx := context.Background()

	if targetContextJSON == "" {
		targetContextJSON = `{"cluster":"minikube"}`
	}
	c := &store.Component{
		Nome:          "componente-de-teste",
		Source:        json.RawMessage(`{"repoUrl":"https://example.com/repo.git","ref":{"type":"branch","value":"main"}}`),
		Resources:     json.RawMessage(`{"requests":{"cpu":"100m"}}`),
		Runtime:       json.RawMessage(`{"workloadKind":"Job"}`),
		TargetContext: json.RawMessage(targetContextJSON),
	}
	if hooksJSON != "" {
		c.Hooks = json.RawMessage(hooksJSON)
	}
	if err := components.Create(ctx, c); err != nil {
		t.Fatalf("criando componente de teste: %v", err)
	}
	// Runner assume um componente já buildado (PhaseBuilt), com digest real.
	if err := components.UpdateBuildStatus(ctx, c.ID, store.PhaseBuilt, "sha256:fakedigest", ""); err != nil {
		t.Fatalf("marcando componente de teste como Built: %v", err)
	}
	c.BuildImageDigest = "sha256:fakedigest"
	return c
}

// jobConditionReactor mantém, por nome de Job, a condição terminal que deve
// ser injetada no objeto assim que ele for criado no fake clientset —
// aplicada dentro do próprio reactor de "create", antes que o tracker
// padrão do fake armazene/devolva o objeto. Isso torna os testes
// determinísticos, sem sleep nem goroutine: applyAndWait já vê a condição
// terminal na primeira chamada, no objeto devolvido por Create.
func newJobConditionReactor(conditions map[string]batchv1.JobCondition) ktesting.ReactionFunc {
	return func(action ktesting.Action) (bool, k8sruntime.Object, error) {
		createAction, ok := action.(ktesting.CreateAction)
		if !ok {
			return false, nil, nil
		}
		job, ok := createAction.GetObject().(*batchv1.Job)
		if !ok {
			return false, nil, nil
		}
		if cond, ok := conditions[job.Name]; ok {
			job.Status.Conditions = []batchv1.JobCondition{cond}
		}
		return false, nil, nil
	}
}

func completeCondition() batchv1.JobCondition {
	return batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}
}

func failedCondition() batchv1.JobCondition {
	return batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}
}

func countCreateActions(clientset *fake.Clientset) int {
	n := 0
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "create" && action.GetResource().Resource == "jobs" {
			n++
		}
	}
	return n
}

func TestRunner_PrincipalSucedidoSemPostRun(t *testing.T) {
	components, executions := newRunnerTestDB(t)
	component := newRunnerTestComponent(t, components, "", "")

	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("create", "jobs", newJobConditionReactor(map[string]batchv1.JobCondition{
		component.ID: completeCondition(),
	}))

	runner := &Runner{
		ClusterProvider: stubClusterProvider{clientset: clientset},
		Components:      components,
		Executions:      executions,
		PollInterval:    time.Millisecond,
	}

	if err := runner.Run(context.Background(), component); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got, err := components.Get(context.Background(), component.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Phase != store.PhaseSucceeded {
		t.Errorf("Phase = %q, esperava %q", got.Phase, store.PhaseSucceeded)
	}
	if n := countCreateActions(clientset); n != 1 {
		t.Errorf("countCreateActions = %d, esperava 1 (sem Job de verificação)", n)
	}
}

func TestRunner_PrincipalSucedidoPostRunSucedido(t *testing.T) {
	components, executions := newRunnerTestDB(t)
	hooks := `{"postRun": [{"name": "verifica", "image": "curlimages/curl:8.9.0", "command": ["true"]}]}`
	component := newRunnerTestComponent(t, components, hooks, "")

	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("create", "jobs", newJobConditionReactor(map[string]batchv1.JobCondition{
		component.ID:              completeCondition(),
		component.ID + "-postrun": completeCondition(),
	}))

	runner := &Runner{
		ClusterProvider: stubClusterProvider{clientset: clientset},
		Components:      components,
		Executions:      executions,
		PollInterval:    time.Millisecond,
	}

	if err := runner.Run(context.Background(), component); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got, err := components.Get(context.Background(), component.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Phase != store.PhaseSucceeded {
		t.Errorf("Phase = %q, esperava %q", got.Phase, store.PhaseSucceeded)
	}
	if n := countCreateActions(clientset); n != 2 {
		t.Errorf("countCreateActions = %d, esperava 2 (principal + verificação)", n)
	}
}

func TestRunner_PostRunFalhaContinueOnErrorFalse(t *testing.T) {
	components, executions := newRunnerTestDB(t)
	hooks := `{"postRun": [{"name": "verifica", "image": "curlimages/curl:8.9.0", "command": ["false"], "continueOnError": false}]}`
	component := newRunnerTestComponent(t, components, hooks, "")

	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("create", "jobs", newJobConditionReactor(map[string]batchv1.JobCondition{
		component.ID:              completeCondition(),
		component.ID + "-postrun": failedCondition(),
	}))

	runner := &Runner{
		ClusterProvider: stubClusterProvider{clientset: clientset},
		Components:      components,
		Executions:      executions,
		PollInterval:    time.Millisecond,
	}

	err := runner.Run(context.Background(), component)
	if err == nil {
		t.Fatal("Run() deveria retornar erro quando o Job de verificação falha com continueOnError:false")
	}

	got, getErr := components.Get(context.Background(), component.ID)
	if getErr != nil {
		t.Fatalf("Get() error = %v", getErr)
	}
	if got.Phase != store.PhaseFailed {
		t.Errorf("Phase = %q, esperava %q", got.Phase, store.PhaseFailed)
	}
	if got.BuildImageDigest != "sha256:fakedigest" {
		t.Errorf("BuildImageDigest = %q, esperava preservado como %q", got.BuildImageDigest, "sha256:fakedigest")
	}
}

func TestRunner_PostRunFalhaContinueOnErrorTrue(t *testing.T) {
	components, executions := newRunnerTestDB(t)
	hooks := `{"postRun": [{"name": "verifica", "image": "curlimages/curl:8.9.0", "command": ["false"], "continueOnError": true}]}`
	component := newRunnerTestComponent(t, components, hooks, "")

	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("create", "jobs", newJobConditionReactor(map[string]batchv1.JobCondition{
		component.ID:              completeCondition(),
		component.ID + "-postrun": failedCondition(),
	}))

	runner := &Runner{
		ClusterProvider: stubClusterProvider{clientset: clientset},
		Components:      components,
		Executions:      executions,
		PollInterval:    time.Millisecond,
	}

	if err := runner.Run(context.Background(), component); err != nil {
		t.Fatalf("Run() error = %v, esperava nil (continueOnError:true não deveria falhar o fluxo)", err)
	}

	got, err := components.Get(context.Background(), component.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Phase != store.PhaseSucceeded {
		t.Errorf("Phase = %q, esperava %q (continueOnError:true preserva o sucesso do principal)", got.Phase, store.PhaseSucceeded)
	}
}

func TestRunner_PrincipalFalhouPostRunAindaDispara(t *testing.T) {
	components, executions := newRunnerTestDB(t)
	hooks := `{"postRun": [{"name": "verifica", "image": "curlimages/curl:8.9.0", "command": ["true"], "continueOnError": true}]}`
	component := newRunnerTestComponent(t, components, hooks, "")

	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("create", "jobs", newJobConditionReactor(map[string]batchv1.JobCondition{
		component.ID:              failedCondition(),
		component.ID + "-postrun": completeCondition(),
	}))

	runner := &Runner{
		ClusterProvider: stubClusterProvider{clientset: clientset},
		Components:      components,
		Executions:      executions,
		PollInterval:    time.Millisecond,
	}

	err := runner.Run(context.Background(), component)
	if err == nil {
		t.Fatal("Run() deveria retornar erro quando o Job principal falha")
	}

	if n := countCreateActions(clientset); n != 2 {
		t.Errorf("countCreateActions = %d, esperava 2 (verificação dispara mesmo com o principal Failed)", n)
	}

	got, getErr := components.Get(context.Background(), component.ID)
	if getErr != nil {
		t.Fatalf("Get() error = %v", getErr)
	}
	if got.Phase != store.PhaseFailed {
		t.Errorf("Phase = %q, esperava %q (verificação bem-sucedida não reverte falha do principal)", got.Phase, store.PhaseFailed)
	}
}

func TestRunner_ErroDeCluster(t *testing.T) {
	components, executions := newRunnerTestDB(t)
	component := newRunnerTestComponent(t, components, "", "")

	runner := &Runner{
		ClusterProvider: stubClusterProvider{err: errors.New("cluster indisponível")},
		Components:      components,
		Executions:      executions,
		PollInterval:    time.Millisecond,
	}

	err := runner.Run(context.Background(), component)
	if err == nil {
		t.Fatal("Run() deveria retornar erro quando o cluster está indisponível")
	}

	got, getErr := components.Get(context.Background(), component.ID)
	if getErr != nil {
		t.Fatalf("Get() error = %v", getErr)
	}
	if got.Phase != store.PhaseFailed {
		t.Errorf("Phase = %q, esperava %q", got.Phase, store.PhaseFailed)
	}
	if got.BuildImageDigest != "sha256:fakedigest" {
		t.Errorf("BuildImageDigest = %q, esperava preservado como %q", got.BuildImageDigest, "sha256:fakedigest")
	}
}

func TestRunner_NamespacePadrao(t *testing.T) {
	components, executions := newRunnerTestDB(t)
	component := newRunnerTestComponent(t, components, "", `{"cluster":"minikube"}`)

	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("create", "jobs", newJobConditionReactor(map[string]batchv1.JobCondition{
		component.ID: completeCondition(),
	}))

	runner := &Runner{
		ClusterProvider: stubClusterProvider{clientset: clientset},
		Components:      components,
		Executions:      executions,
		PollInterval:    time.Millisecond,
	}

	if err := runner.Run(context.Background(), component); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	job, err := clientset.BatchV1().Jobs("default").Get(context.Background(), component.ID, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Job não encontrado no namespace default: %v", err)
	}
	if job.Namespace != "default" {
		t.Errorf("job.Namespace = %q, esperava %q", job.Namespace, "default")
	}
}

func TestRunner_applyAndWait_SondaAteConditionAparecer(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	calls := 0
	clientset.PrependReactor("get", "jobs", func(action ktesting.Action) (bool, k8sruntime.Object, error) {
		calls++
		getAction := action.(ktesting.GetAction)
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: getAction.GetName(), Namespace: getAction.GetNamespace()},
		}
		if calls >= 2 {
			job.Status.Conditions = []batchv1.JobCondition{completeCondition()}
		}
		return true, job, nil
	})

	runner := &Runner{
		ClusterProvider: stubClusterProvider{clientset: clientset},
		PollInterval:    time.Millisecond,
	}

	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "sonda-teste", Namespace: "default"}}
	phase, err := runner.applyAndWait(context.Background(), clientset, job)
	if err != nil {
		t.Fatalf("applyAndWait() error = %v", err)
	}
	if phase != store.PhaseSucceeded {
		t.Errorf("phase = %q, esperava %q", phase, store.PhaseSucceeded)
	}
	if calls < 2 {
		t.Errorf("calls = %d, esperava >= 2 (deveria sondar mais de uma vez)", calls)
	}
}

func TestDetermineFinalPhase(t *testing.T) {
	tests := []struct {
		name            string
		mainPhase       string
		postRunPhase    string
		continueOnError bool
		want            string
	}{
		{"principal Succeeded, sem postRun", store.PhaseSucceeded, "", false, store.PhaseSucceeded},
		{"principal Succeeded, postRun Succeeded", store.PhaseSucceeded, store.PhaseSucceeded, false, store.PhaseSucceeded},
		{"principal Succeeded, postRun Failed, continueOnError false", store.PhaseSucceeded, store.PhaseFailed, false, store.PhaseFailed},
		{"principal Succeeded, postRun Failed, continueOnError true", store.PhaseSucceeded, store.PhaseFailed, true, store.PhaseSucceeded},
		{"principal Failed, postRun Succeeded", store.PhaseFailed, store.PhaseSucceeded, false, store.PhaseFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineFinalPhase(tt.mainPhase, tt.postRunPhase, tt.continueOnError)
			if got != tt.want {
				t.Errorf("determineFinalPhase(%q, %q, %v) = %q, esperava %q", tt.mainPhase, tt.postRunPhase, tt.continueOnError, got, tt.want)
			}
		})
	}
}
