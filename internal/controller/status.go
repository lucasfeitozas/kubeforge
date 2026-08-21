package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/lucasfeitozas/kubeforge/internal/k8s"
	"github.com/lucasfeitozas/kubeforge/internal/store"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ErrPodNotFound indica que o Job principal do componente ainda não tem
// nenhum Pod no cluster — Runner.Run nunca foi chamado para ele, ou o Pod
// já foi removido (TTL/cleanup, fora do escopo desta história).
var ErrPodNotFound = errors.New("nenhum pod encontrado para o job do componente")

// mainContainerName é o container observado por GetPodStatus/StreamPodLogs
// — mesmo nome fixado por BuildJob para o container principal do Job.
const mainContainerName = "main"

// defaultLogPollInterval é usado por StreamPodLogs quando follow=true e
// pollInterval <= 0.
const defaultLogPollInterval = 2 * time.Second

// PodStatus é a projeção do status do Pod do Job principal de um
// componente (E4.S4, AC1): reflete corev1.PodStatus.Phase, que já usa o
// vocabulário Pending/Running/Succeeded/Failed pedido no critério de
// aceite (mais Unknown, nativo do Kubernetes).
type PodStatus struct {
	PodName string
	Phase   string
	Reason  string
	Message string
}

// GetPodStatus consulta ao vivo o status do Pod do Job principal do
// componente componentID (E4.S4, AC1: "Endpoint/consulta que reflete o
// status atual do Job/Pod"), sem exigir kubectl direto no cluster: resolve
// targetContext (mesma interpretação usada por Runner.Run), localiza o Pod
// mais recente criado pelo Job e projeta seu status.
//
// Retorna store.ErrComponentNotFound se componentID não existir, e
// ErrPodNotFound se o Job ainda não tiver nenhum Pod no cluster-alvo.
func GetPodStatus(ctx context.Context, provider k8s.ClusterProvider, components store.ComponentRepository, componentID string) (*PodStatus, error) {
	clientset, namespace, jobName, err := locateComponentJob(ctx, provider, components, componentID)
	if err != nil {
		return nil, err
	}

	pod, err := latestJobPod(ctx, clientset, namespace, jobName)
	if err != nil {
		return nil, err
	}

	return &PodStatus{
		PodName: pod.Name,
		Phase:   string(pod.Status.Phase),
		Reason:  pod.Status.Reason,
		Message: pod.Status.Message,
	}, nil
}

// StreamPodLogs escreve em w os logs do container principal do Pod do Job
// do componente componentID (E4.S4, AC2: "Stream ou tail de logs disponível
// via API").
//
// follow=false escreve o snapshot atual dos logs (respeitando tailLines, se
// informado) e retorna. follow=true reenvia logs incrementalmente a cada
// pollInterval (default defaultLogPollInterval), usando o timestamp da
// última leitura como PodLogOptions.SinceTime, até o Pod atingir uma fase
// terminal (Succeeded/Failed) ou ctx ser cancelado (ex.: cliente HTTP
// desconectou) — ver docs/adrs/0008-status-e-logs-http-poll.md para a
// justificativa de não usar PodLogOptions.Follow diretamente.
//
// Retorna os mesmos erros de GetPodStatus (store.ErrComponentNotFound,
// ErrPodNotFound) antes de qualquer escrita em w; erros que ocorram depois
// de já ter escrito algum conteúdo em w (ex.: cluster ficou inacessível no
// meio do polling) também são retornados, cabendo ao chamador decidir como
// reportá-los visto que a resposta HTTP já pode estar em andamento.
func StreamPodLogs(ctx context.Context, provider k8s.ClusterProvider, components store.ComponentRepository, componentID string, w io.Writer, follow bool, tailLines *int64, pollInterval time.Duration) error {
	clientset, namespace, jobName, err := locateComponentJob(ctx, provider, components, componentID)
	if err != nil {
		return err
	}

	pod, err := latestJobPod(ctx, clientset, namespace, jobName)
	if err != nil {
		return err
	}

	since, err := fetchLogs(ctx, clientset, namespace, pod.Name, tailLines, nil, w)
	if err != nil {
		return err
	}
	if !follow {
		return nil
	}

	if pollInterval <= 0 {
		pollInterval = defaultLogPollInterval
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		pod, err = clientset.CoreV1().Pods(namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("consultando status do pod %q: %w", pod.Name, err)
		}

		since, err = fetchLogs(ctx, clientset, namespace, pod.Name, nil, &since, w)
		if err != nil {
			return err
		}

		if podTerminal(pod.Status.Phase) {
			return nil
		}
	}
}

// locateComponentJob resolve o clientset, o namespace e o nome do Job
// principal (== component.ID, ver BuildJob) associados a componentID,
// reaproveitando parseTargetContext (mesma interpretação de
// spec.targetContext usada por Runner.Run) para não duplicar essa lógica.
func locateComponentJob(ctx context.Context, provider k8s.ClusterProvider, components store.ComponentRepository, componentID string) (kubernetes.Interface, string, string, error) {
	component, err := components.Get(ctx, componentID)
	if err != nil {
		return nil, "", "", err
	}

	tc, err := parseTargetContext(component.TargetContext)
	if err != nil {
		return nil, "", "", fmt.Errorf("interpretando targetContext do componente %q: %w", componentID, err)
	}

	clientset, err := provider.GetClientset(ctx, tc.Cluster)
	if err != nil {
		return nil, "", "", fmt.Errorf("obtendo clientset do cluster %q: %w", tc.Cluster, err)
	}

	return clientset, tc.Namespace, component.ID, nil
}

// latestJobPod lista os Pods do Job jobName em namespace (rótulo
// batchv1.JobNameLabel, aplicado nativamente pelo controller do Job do
// cluster) e devolve o mais recente por CreationTimestamp — um Job com
// backoffLimit > 0 pode ter mais de um Pod ao longo de retries; o mais
// recente é o que reflete o estado atual da execução.
func latestJobPod(ctx context.Context, clientset kubernetes.Interface, namespace, jobName string) (*corev1.Pod, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: batchv1.JobNameLabel + "=" + jobName,
	})
	if err != nil {
		return nil, fmt.Errorf("listando pods do job %q no namespace %q: %w", jobName, namespace, err)
	}
	if len(pods.Items) == 0 {
		return nil, ErrPodNotFound
	}

	latest := &pods.Items[0]
	for i := 1; i < len(pods.Items); i++ {
		if pods.Items[i].CreationTimestamp.After(latest.CreationTimestamp.Time) {
			latest = &pods.Items[i]
		}
	}
	return latest, nil
}

// fetchLogs busca (sem PodLogOptions.Follow) os logs do container principal
// do pod podName, restritos a tailLines linhas mais recentes (só usado na
// primeira chamada de StreamPodLogs, tailLines == nil nas seguintes) ou a
// partir de sinceTime (nil na primeira chamada). Copia o conteúdo para w e
// devolve o timestamp a usar como sinceTime na chamada seguinte.
//
// A granularidade de 1s de PodLogOptions.SinceTime pode reenviar até ~1s de
// linhas já mostradas na leitura seguinte — limitação aceita, documentada
// na ADR 0008, e sem impacto no critério de aceite (acompanhar o log, não
// garantir exactly-once).
func fetchLogs(ctx context.Context, clientset kubernetes.Interface, namespace, podName string, tailLines *int64, sinceTime *time.Time, w io.Writer) (time.Time, error) {
	fetchedAt := time.Now()

	opts := &corev1.PodLogOptions{
		Container: mainContainerName,
		TailLines: tailLines,
	}
	if sinceTime != nil {
		t := metav1.NewTime(*sinceTime)
		opts.SinceTime = &t
	}

	stream, err := clientset.CoreV1().Pods(namespace).GetLogs(podName, opts).Stream(ctx)
	if err != nil {
		return fetchedAt, fmt.Errorf("obtendo logs do pod %q: %w", podName, err)
	}
	defer stream.Close()

	if _, err := io.Copy(w, stream); err != nil {
		return fetchedAt, fmt.Errorf("copiando logs do pod %q: %w", podName, err)
	}
	return fetchedAt, nil
}

// podTerminal indica se phase é uma fase terminal de Pod — mesmo par
// Succeeded/Failed observado por Runner.applyAndWait no nível do Job.
func podTerminal(phase corev1.PodPhase) bool {
	return phase == corev1.PodSucceeded || phase == corev1.PodFailed
}
