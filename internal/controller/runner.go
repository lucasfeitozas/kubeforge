package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lucasfeitozas/kubeforge/internal/k8s"
	"github.com/lucasfeitozas/kubeforge/internal/store"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// executionPhaseRunning/Succeeded/Failed espelham o CHECK da tabela
// executions (Pending, Running, Succeeded, Failed), mesmos valores usados
// por internal/build.Broker — ver
// internal/store/migrations/0001_create_components_and_executions.up.sql.
const (
	executionPhaseRunning   = "Running"
	executionPhaseSucceeded = "Succeeded"
	executionPhaseFailed    = "Failed"
)

// defaultPollInterval é usado quando Runner.PollInterval não é definido.
const defaultPollInterval = 2 * time.Second

// defaultNamespace é aplicado quando targetContext.namespace não é
// informado no Componente.
const defaultNamespace = "default"

// runnerTargetContextBlock espelha os campos de spec.targetContext
// necessários para aplicar Jobs no cluster (docs/ARCHITECTURE.md §2.1),
// parseado no mesmo estilo dos blocos privados de internal/build/broker.go.
type runnerTargetContextBlock struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
}

// Runner orquestra a execução de um Componente já buildado (E4.S3): aplica
// o Job principal no cluster-alvo, sonda sua fase até Succeeded/Failed e,
// se hooks.postRun estiver definido, aplica e sonda também o Job de
// verificação, persistindo a fase final em Component/Execution. Segue o
// mesmo idioma síncrono de internal/build.Broker — não há
// controller-runtime/reconcile loop neste projeto.
type Runner struct {
	ClusterProvider k8s.ClusterProvider
	Components      store.ComponentRepository
	Executions      store.ExecutionRepository
	// PollInterval controla o intervalo entre consultas de status de um
	// Job em execução. Usa defaultPollInterval quando <= 0.
	PollInterval time.Duration
}

// Run cria uma Execution para component e conduz o ciclo completo:
// aplica o Job principal, aguarda sua fase terminal, dispara o Job de
// verificação (hooks.postRun) independente do resultado do principal, e
// persiste a fase final combinada (ver determineFinalPhase) em
// component.Phase e na Execution criada.
//
// Run retorna erro tanto para falhas de infraestrutura (targetContext
// inválido, cluster inacessível, Job inaplicável) quanto quando a fase
// final observada é Failed — mesma convenção de internal/build.Broker.Run:
// Run só retorna nil quando o estado persistido é de sucesso.
func (r *Runner) Run(ctx context.Context, component *store.Component) error {
	execution := &store.Execution{ComponentID: component.ID}
	if err := r.Executions.Create(ctx, execution); err != nil {
		return fmt.Errorf("criando execution para o componente %q: %w", component.ID, err)
	}

	startedAt := time.Now().UTC().Truncate(time.Millisecond)
	if err := r.Components.UpdateBuildStatus(ctx, component.ID, store.PhaseRunning, component.BuildImageDigest, ""); err != nil {
		return fmt.Errorf("atualizando status do componente %q para Running: %w", component.ID, err)
	}
	if err := r.Executions.UpdatePhase(ctx, execution.ID, executionPhaseRunning, &startedAt, nil); err != nil {
		return fmt.Errorf("atualizando fase da execution %q para Running: %w", execution.ID, err)
	}

	tc, err := parseTargetContext(component.TargetContext)
	if err != nil {
		return r.fail(ctx, component.ID, execution.ID, component.BuildImageDigest, startedAt,
			fmt.Errorf("interpretando targetContext do componente %q: %w", component.ID, err))
	}

	clientset, err := r.ClusterProvider.GetClientset(ctx, tc.Cluster)
	if err != nil {
		return r.fail(ctx, component.ID, execution.ID, component.BuildImageDigest, startedAt,
			fmt.Errorf("obtendo clientset do cluster %q: %w", tc.Cluster, err))
	}

	mainJob, err := BuildJob(component)
	if err != nil {
		return r.fail(ctx, component.ID, execution.ID, component.BuildImageDigest, startedAt,
			fmt.Errorf("montando Job principal do componente %q: %w", component.ID, err))
	}
	mainJob.Namespace = tc.Namespace

	mainPhase, err := r.applyAndWait(ctx, clientset, mainJob)
	if err != nil {
		return r.fail(ctx, component.ID, execution.ID, component.BuildImageDigest, startedAt,
			fmt.Errorf("executando Job principal do componente %q: %w", component.ID, err))
	}

	// hooks.postRun é disparado independente de mainPhase ser Succeeded ou
	// Failed — critério de aceite E4.S3/AC1: "Controller observa phase
	// Succeeded/Failed do Job principal e dispara o Job de verificação".
	postRunPlan, err := BuildPostRunJob(component)
	if err != nil {
		return r.fail(ctx, component.ID, execution.ID, component.BuildImageDigest, startedAt,
			fmt.Errorf("montando Job de verificação do componente %q: %w", component.ID, err))
	}

	finalPhase := mainPhase
	if postRunPlan.Job != nil {
		postRunPlan.Job.Namespace = tc.Namespace
		postRunPhase, err := r.applyAndWait(ctx, clientset, postRunPlan.Job)
		if err != nil {
			return r.fail(ctx, component.ID, execution.ID, component.BuildImageDigest, startedAt,
				fmt.Errorf("executando Job de verificação do componente %q: %w", component.ID, err))
		}
		finalPhase = determineFinalPhase(mainPhase, postRunPhase, postRunPlan.ContinueOnError)
	}

	return r.finalize(ctx, component.ID, execution.ID, component.BuildImageDigest, startedAt, finalPhase)
}

// fail marca Component e Execution como Failed por causa de um erro de
// infraestrutura/interpretação (não uma fase Failed legitimamente
// observada num Job — ver finalize), e devolve causeErr para o chamador.
func (r *Runner) fail(ctx context.Context, componentID, executionID, digest string, startedAt time.Time, causeErr error) error {
	completedAt := time.Now().UTC().Truncate(time.Millisecond)

	if err := r.Components.UpdateBuildStatus(ctx, componentID, store.PhaseFailed, digest, causeErr.Error()); err != nil {
		return fmt.Errorf("atualizando status do componente %q para Failed (após erro %q): %w", componentID, causeErr, err)
	}
	if err := r.Executions.UpdatePhase(ctx, executionID, executionPhaseFailed, &startedAt, &completedAt); err != nil {
		return fmt.Errorf("atualizando fase da execution %q para Failed (após erro %q): %w", executionID, causeErr, err)
	}
	return causeErr
}

// finalize persiste a fase final (Succeeded ou Failed) já observada nos
// Jobs principal/verificação, retornando erro não-nil sse finalPhase ==
// store.PhaseFailed — mesma convenção de Broker.Run/fail.
func (r *Runner) finalize(ctx context.Context, componentID, executionID, digest string, startedAt time.Time, finalPhase string) error {
	completedAt := time.Now().UTC().Truncate(time.Millisecond)

	executionPhase, errMsg := executionPhaseSucceeded, ""
	if finalPhase == store.PhaseFailed {
		executionPhase = executionPhaseFailed
		errMsg = fmt.Sprintf("execução do componente %q terminou como Failed (Job principal ou de verificação falhou)", componentID)
	}

	if err := r.Components.UpdateBuildStatus(ctx, componentID, finalPhase, digest, errMsg); err != nil {
		return fmt.Errorf("atualizando status do componente %q para %q: %w", componentID, finalPhase, err)
	}
	if err := r.Executions.UpdatePhase(ctx, executionID, executionPhase, &startedAt, &completedAt); err != nil {
		return fmt.Errorf("atualizando fase da execution %q para %q: %w", executionID, executionPhase, err)
	}
	if errMsg != "" {
		return errors.New(errMsg)
	}
	return nil
}

// applyAndWait cria job no cluster (namespace já preenchido pelo chamador)
// e sonda seu status a cada r.PollInterval até status.conditions reportar
// JobComplete=True ou JobFailed=True, ou até ctx ser cancelado.
func (r *Runner) applyAndWait(ctx context.Context, clientset kubernetes.Interface, job *batchv1.Job) (string, error) {
	created, err := clientset.BatchV1().Jobs(job.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("aplicando Job %q no namespace %q: %w", job.Name, job.Namespace, err)
	}

	interval := r.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if phase, done := jobTerminalPhase(created); done {
			return phase, nil
		}

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("aguardando término do Job %q: %w", job.Name, ctx.Err())
		case <-ticker.C:
			created, err = clientset.BatchV1().Jobs(job.Namespace).Get(ctx, job.Name, metav1.GetOptions{})
			if err != nil {
				return "", fmt.Errorf("consultando status do Job %q: %w", job.Name, err)
			}
		}
	}
}

// jobTerminalPhase inspeciona status.conditions do Job em busca de uma
// condição terminal. done é false enquanto o Job ainda está em andamento.
func jobTerminalPhase(job *batchv1.Job) (phase string, done bool) {
	for _, cond := range job.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		switch cond.Type {
		case batchv1.JobComplete:
			return store.PhaseSucceeded, true
		case batchv1.JobFailed:
			return store.PhaseFailed, true
		}
	}
	return "", false
}

// determineFinalPhase combina a fase do Job principal com a do Job de
// verificação (E4.S3, AC2): uma falha do Job de verificação só sobrepõe um
// Job principal bem-sucedido quando continueOnError é false; caso
// contrário — continueOnError true, ou o Job de verificação não falhou —
// a fase final reflete só o Job principal, inclusive quando ele já é
// Failed (a verificação não "salva" uma execução que já falhou).
func determineFinalPhase(mainPhase, postRunPhase string, continueOnError bool) string {
	if postRunPhase == store.PhaseFailed && !continueOnError {
		return store.PhaseFailed
	}
	return mainPhase
}

func parseTargetContext(raw json.RawMessage) (runnerTargetContextBlock, error) {
	var tc runnerTargetContextBlock
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &tc); err != nil {
			return tc, fmt.Errorf("targetContext inválido: %w", err)
		}
	}
	if tc.Namespace == "" {
		tc.Namespace = defaultNamespace
	}
	return tc, nil
}
