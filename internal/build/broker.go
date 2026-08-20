package build

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lucasfeitozas/kubeforge/internal/store"
)

// brokerSourceBlock espelha os campos do bloco source (docs/ARCHITECTURE.md
// §2.2) necessários para montar um CloneSpec, no mesmo estilo dos blocos
// privados de internal/store/component_validation.go.
type brokerSourceBlock struct {
	RepoURL              string          `json:"repoUrl"`
	Ref                  *brokerRefBlock `json:"ref"`
	SubPath              string          `json:"subPath"`
	CredentialsSecretRef string          `json:"credentialsSecretRef"`
}

type brokerRefBlock struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// brokerBuildBlock espelha os campos do bloco build (opcional) necessários
// para montar um BuildSpec.
type brokerBuildBlock struct {
	ImageRepository  string `json:"imageRepository"`
	ImageTagStrategy string `json:"imageTagStrategy"`
}

// executionRunningPhase e demais valores de fase de Execution seguem o CHECK
// existente da tabela executions (Pending, Running, Succeeded, Failed) — ver
// internal/store/migrations/0001_create_components_and_executions.up.sql.
const (
	executionPhaseRunning   = "Running"
	executionPhaseSucceeded = "Succeeded"
	executionPhaseFailed    = "Failed"
)

// Broker orquestra o clone e a build de um Componente (E3.S1/E3.S2),
// persistindo o resultado tanto na Execution quanto no status do Componente
// (E3.S3): Pending -> Building/Running -> Built|Failed.
type Broker struct {
	Cloner     Cloner
	Builder    Builder
	Components store.ComponentRepository
	Executions store.ExecutionRepository
}

// Run executa o ciclo completo de build de component: cria uma Execution,
// clona o código-fonte em cloneDestDir e builda a imagem, atualizando
// status.phase/status.buildImageDigest do Componente e a fase da Execution
// ao longo do processo. cloneDestDir precisa existir e estar vazio (mesmo
// contrato de Cloner.Clone).
//
// Run sempre retorna o erro de interpretação/clone/build encontrado (se
// houver), já tendo persistido o estado Failed correspondente no Componente
// e na Execution — quem chama Run não precisa (mas pode) reconsultá-los para
// saber o que houve.
func (b *Broker) Run(ctx context.Context, component *store.Component, cloneDestDir string) error {
	execution := &store.Execution{ComponentID: component.ID}
	if err := b.Executions.Create(ctx, execution); err != nil {
		return fmt.Errorf("criando execution para o componente %q: %w", component.ID, err)
	}

	startedAt := time.Now().UTC().Truncate(time.Millisecond)
	if err := b.Components.UpdateBuildStatus(ctx, component.ID, store.PhaseBuilding, "", ""); err != nil {
		return fmt.Errorf("atualizando status do componente %q para Building: %w", component.ID, err)
	}
	if err := b.Executions.UpdatePhase(ctx, execution.ID, executionPhaseRunning, &startedAt, nil); err != nil {
		return fmt.Errorf("atualizando fase da execution %q para Running: %w", execution.ID, err)
	}

	cloneSpec, err := buildCloneSpec(component.Source)
	if err != nil {
		return b.fail(ctx, component.ID, execution.ID, startedAt, fmt.Errorf("interpretando source do componente %q: %w", component.ID, err))
	}

	cloneResult, err := b.Cloner.Clone(ctx, cloneSpec, cloneDestDir)
	if err != nil {
		return b.fail(ctx, component.ID, execution.ID, startedAt, fmt.Errorf("clonando componente %q: %w", component.ID, err))
	}

	buildSpec, err := buildBuildSpec(component.Build, *cloneResult)
	if err != nil {
		return b.fail(ctx, component.ID, execution.ID, startedAt, fmt.Errorf("interpretando build do componente %q: %w", component.ID, err))
	}

	buildResult, buildErr := b.Builder.Build(ctx, buildSpec)

	// O log/tag são persistidos mesmo em caso de falha: BuildResult nunca é
	// nil quando o erro vem de um `docker build`/`docker image inspect` que
	// rodou (ver docker_builder.go), preservando o log para diagnóstico.
	if buildResult != nil {
		if logErr := b.Executions.UpdateBuildLog(ctx, execution.ID, buildResult.ImageTag, buildResult.Log); logErr != nil {
			return fmt.Errorf("persistindo log de build da execution %q: %w", execution.ID, logErr)
		}
	}

	if buildErr != nil {
		return b.fail(ctx, component.ID, execution.ID, startedAt, fmt.Errorf("buildando componente %q: %w", component.ID, buildErr))
	}

	completedAt := time.Now().UTC().Truncate(time.Millisecond)
	if err := b.Components.UpdateBuildStatus(ctx, component.ID, store.PhaseBuilt, buildResult.Digest, ""); err != nil {
		return fmt.Errorf("atualizando status do componente %q para Built: %w", component.ID, err)
	}
	if err := b.Executions.UpdatePhase(ctx, execution.ID, executionPhaseSucceeded, &startedAt, &completedAt); err != nil {
		return fmt.Errorf("atualizando fase da execution %q para Succeeded: %w", execution.ID, err)
	}
	return nil
}

// fail marca o Componente como Failed (com a mensagem de causeErr) e a
// Execution como Failed, e devolve causeErr para o chamador de Run.
func (b *Broker) fail(ctx context.Context, componentID, executionID string, startedAt time.Time, causeErr error) error {
	completedAt := time.Now().UTC().Truncate(time.Millisecond)

	if err := b.Components.UpdateBuildStatus(ctx, componentID, store.PhaseFailed, "", causeErr.Error()); err != nil {
		return fmt.Errorf("atualizando status do componente %q para Failed (após erro %q): %w", componentID, causeErr, err)
	}
	if err := b.Executions.UpdatePhase(ctx, executionID, executionPhaseFailed, &startedAt, &completedAt); err != nil {
		return fmt.Errorf("atualizando fase da execution %q para Failed (após erro %q): %w", executionID, causeErr, err)
	}
	return causeErr
}

func buildCloneSpec(rawSource json.RawMessage) (CloneSpec, error) {
	var s brokerSourceBlock
	if err := json.Unmarshal(rawSource, &s); err != nil {
		return CloneSpec{}, fmt.Errorf("source inválido: %w", err)
	}
	spec := CloneSpec{
		RepoURL:              s.RepoURL,
		SubPath:              s.SubPath,
		CredentialsSecretRef: s.CredentialsSecretRef,
	}
	if s.Ref != nil {
		spec.Ref = RefSpec{Type: s.Ref.Type, Value: s.Ref.Value}
	}
	return spec, nil
}

func buildBuildSpec(rawBuild json.RawMessage, cloneResult CloneResult) (BuildSpec, error) {
	var bb brokerBuildBlock
	if len(rawBuild) > 0 {
		if err := json.Unmarshal(rawBuild, &bb); err != nil {
			return BuildSpec{}, fmt.Errorf("build inválido: %w", err)
		}
	}
	return BuildSpec{
		CloneResult:      cloneResult,
		ImageRepository:  bb.ImageRepository,
		ImageTagStrategy: bb.ImageTagStrategy,
	}, nil
}
