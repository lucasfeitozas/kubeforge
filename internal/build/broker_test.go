package build

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lucasfeitozas/kubeforge/internal/store"
)

// stubCloner é um Cloner fake controlado diretamente pelo teste, seguindo a
// mesma convenção de stubDockerEnvResolver em docker_builder_test.go: sem
// lib de mock, apenas um struct simples.
type stubCloner struct {
	result *CloneResult
	err    error
	called bool
}

func (s *stubCloner) Clone(ctx context.Context, spec CloneSpec, destDir string) (*CloneResult, error) {
	s.called = true
	return s.result, s.err
}

// failIfCalledCloner falha o teste se Clone for invocado — usado para
// asserir que o Broker não chega a clonar quando a interpretação do source
// falha antes disso.
type failIfCalledCloner struct{ t *testing.T }

func (f failIfCalledCloner) Clone(ctx context.Context, spec CloneSpec, destDir string) (*CloneResult, error) {
	f.t.Fatal("Cloner.Clone não deveria ter sido chamado")
	return nil, nil
}

type stubBuilder struct {
	result  *BuildResult
	err     error
	called  bool
	gotSpec BuildSpec
}

func (s *stubBuilder) Build(ctx context.Context, spec BuildSpec) (*BuildResult, error) {
	s.called = true
	s.gotSpec = spec
	return s.result, s.err
}

// failIfCalledBuilder falha o teste se Build for invocado — usado para
// asserir que o Broker não chega a buildar quando o clone falha.
type failIfCalledBuilder struct{ t *testing.T }

func (f failIfCalledBuilder) Build(ctx context.Context, spec BuildSpec) (*BuildResult, error) {
	f.t.Fatal("Builder.Build não deveria ter sido chamado")
	return nil, nil
}

func newBrokerTestDB(t *testing.T) (store.ComponentRepository, store.ExecutionRepository) {
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

func newBrokerTestComponent(t *testing.T, components store.ComponentRepository, sourceJSON string) *store.Component {
	t.Helper()
	c := &store.Component{
		Nome:          "componente-de-teste",
		Source:        json.RawMessage(sourceJSON),
		Build:         json.RawMessage(`{"imageRepository":"kubeforge/carga-cpu","imageTagStrategy":"commit-sha"}`),
		Resources:     json.RawMessage(`{"requests":{"cpu":"100m"}}`),
		Runtime:       json.RawMessage(`{"workloadKind":"Pod"}`),
		TargetContext: json.RawMessage(`{"cluster":"minikube"}`),
	}
	if err := components.Create(context.Background(), c); err != nil {
		t.Fatalf("criando componente de teste: %v", err)
	}
	return c
}

const validSourceJSON = `{"repoUrl":"https://example.com/repo.git","ref":{"type":"branch","value":"main"}}`

func TestBroker_RunSucesso(t *testing.T) {
	ctx := context.Background()
	components, executions := newBrokerTestDB(t)
	component := newBrokerTestComponent(t, components, validSourceJSON)

	cloneResult := &CloneResult{Dir: "/tmp/src", CommitSHA: "abcdef1234567890", DockerfilePath: "/tmp/src/Dockerfile"}
	buildResult := &BuildResult{ImageTag: "kubeforge/carga-cpu:abcdef123456", CommitSHA: cloneResult.CommitSHA, Digest: "sha256:xyz", Log: "log de build ok"}

	broker := &Broker{
		Cloner:     &stubCloner{result: cloneResult},
		Builder:    &stubBuilder{result: buildResult},
		Components: components,
		Executions: executions,
	}

	if err := broker.Run(ctx, component, t.TempDir(), false); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got, err := components.Get(ctx, component.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Phase != store.PhaseBuilt {
		t.Errorf("Phase = %q, want %q", got.Phase, store.PhaseBuilt)
	}
	if got.BuildImageDigest != "sha256:xyz" {
		t.Errorf("BuildImageDigest = %q, want %q", got.BuildImageDigest, "sha256:xyz")
	}
	if got.ErrorMessage != "" {
		t.Errorf("ErrorMessage = %q, want vazio", got.ErrorMessage)
	}

	execs, err := executions.List(ctx, component.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(execs) != 1 {
		t.Fatalf("List() = %d executions, want 1", len(execs))
	}
	e := execs[0]
	if e.Phase != "Succeeded" {
		t.Errorf("Execution Phase = %q, want %q", e.Phase, "Succeeded")
	}
	if e.ImageTag != buildResult.ImageTag {
		t.Errorf("Execution ImageTag = %q, want %q", e.ImageTag, buildResult.ImageTag)
	}
	if e.BuildLog != buildResult.Log {
		t.Errorf("Execution BuildLog = %q, want %q", e.BuildLog, buildResult.Log)
	}
	if e.StartedAt == nil || e.CompletedAt == nil {
		t.Errorf("Execution StartedAt/CompletedAt = %v/%v, want ambos preenchidos", e.StartedAt, e.CompletedAt)
	}
}

func TestBroker_RunFalhaNoClone(t *testing.T) {
	ctx := context.Background()
	components, executions := newBrokerTestDB(t)
	component := newBrokerTestComponent(t, components, validSourceJSON)

	broker := &Broker{
		Cloner:     &stubCloner{err: errors.New("clone falhou: repositório inacessível")},
		Builder:    failIfCalledBuilder{t: t},
		Components: components,
		Executions: executions,
	}

	err := broker.Run(ctx, component, t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "clone falhou") {
		t.Fatalf("Run() error = %v, want erro contendo %q", err, "clone falhou")
	}

	got, err := components.Get(ctx, component.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Phase != store.PhaseFailed {
		t.Errorf("Phase = %q, want %q", got.Phase, store.PhaseFailed)
	}
	if !strings.Contains(got.ErrorMessage, "clone falhou") {
		t.Errorf("ErrorMessage = %q, want conter %q", got.ErrorMessage, "clone falhou")
	}
	if got.BuildImageDigest != "" {
		t.Errorf("BuildImageDigest = %q, want vazio", got.BuildImageDigest)
	}

	execs, err := executions.List(ctx, component.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(execs) != 1 || execs[0].Phase != "Failed" {
		t.Fatalf("List() = %+v, want 1 execution com Phase=Failed", execs)
	}
	if execs[0].ImageTag != "" || execs[0].BuildLog != "" {
		t.Errorf("Execution ImageTag/BuildLog = %q/%q, want vazios (Builder nunca chamado)", execs[0].ImageTag, execs[0].BuildLog)
	}
}

func TestBroker_RunFalhaNoBuild(t *testing.T) {
	ctx := context.Background()
	components, executions := newBrokerTestDB(t)
	component := newBrokerTestComponent(t, components, validSourceJSON)

	cloneResult := &CloneResult{Dir: "/tmp/src", CommitSHA: "abcdef1234567890", DockerfilePath: "/tmp/src/Dockerfile"}
	// Mesma forma de docker_builder.go: em falha, BuildResult não-nil
	// preserva ImageTag/Log, mas sem Digest.
	buildResult := &BuildResult{ImageTag: "kubeforge/carga-cpu:abcdef123456", Log: "log de build com erro"}

	broker := &Broker{
		Cloner:     &stubCloner{result: cloneResult},
		Builder:    &stubBuilder{result: buildResult, err: errors.New("docker build falhou: exit status 1")},
		Components: components,
		Executions: executions,
	}

	err := broker.Run(ctx, component, t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "docker build falhou") {
		t.Fatalf("Run() error = %v, want erro contendo %q", err, "docker build falhou")
	}

	got, err := components.Get(ctx, component.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Phase != store.PhaseFailed {
		t.Errorf("Phase = %q, want %q", got.Phase, store.PhaseFailed)
	}
	if !strings.Contains(got.ErrorMessage, "docker build falhou") {
		t.Errorf("ErrorMessage = %q, want conter %q", got.ErrorMessage, "docker build falhou")
	}

	execs, err := executions.List(ctx, component.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(execs) != 1 {
		t.Fatalf("List() = %d executions, want 1", len(execs))
	}
	e := execs[0]
	if e.Phase != "Failed" {
		t.Errorf("Execution Phase = %q, want %q", e.Phase, "Failed")
	}
	if e.BuildLog != buildResult.Log {
		t.Errorf("Execution BuildLog = %q, want %q (log deve ser persistido mesmo em falha)", e.BuildLog, buildResult.Log)
	}
	if e.ImageTag != buildResult.ImageTag {
		t.Errorf("Execution ImageTag = %q, want %q", e.ImageTag, buildResult.ImageTag)
	}
}

func TestBroker_RunSourceInvalido(t *testing.T) {
	ctx := context.Background()
	components, executions := newBrokerTestDB(t)
	component := newBrokerTestComponent(t, components, validSourceJSON)
	// Corrompe o source após a criação (Validate() na criação exigiria um
	// source bem-formado) para simular um dado malformado chegando ao Broker.
	component.Source = json.RawMessage(`not json`)

	broker := &Broker{
		Cloner:     failIfCalledCloner{t: t},
		Builder:    failIfCalledBuilder{t: t},
		Components: components,
		Executions: executions,
	}

	err := broker.Run(ctx, component, t.TempDir(), false)
	if err == nil {
		t.Fatal("Run() error = nil, want erro por source inválido")
	}

	got, err := components.Get(ctx, component.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Phase != store.PhaseFailed {
		t.Errorf("Phase = %q, want %q", got.Phase, store.PhaseFailed)
	}
}

func TestBroker_RunPropagaNoCache(t *testing.T) {
	ctx := context.Background()
	components, executions := newBrokerTestDB(t)
	component := newBrokerTestComponent(t, components, validSourceJSON)

	cloneResult := &CloneResult{Dir: "/tmp/src", CommitSHA: "abcdef1234567890", DockerfilePath: "/tmp/src/Dockerfile"}
	buildResult := &BuildResult{ImageTag: "kubeforge/carga-cpu:abcdef123456", Digest: "sha256:xyz"}
	builder := &stubBuilder{result: buildResult}

	broker := &Broker{
		Cloner:     &stubCloner{result: cloneResult},
		Builder:    builder,
		Components: components,
		Executions: executions,
	}

	if err := broker.Run(ctx, component, t.TempDir(), true); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !builder.gotSpec.NoCache {
		t.Error("BuildSpec.NoCache = false, want true (propagado de Run)")
	}
}
