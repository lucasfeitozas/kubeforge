package controller

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lucasfeitozas/kubeforge/internal/store"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// newJobPod cria um Pod fake com o rótulo batchv1.JobNameLabel==jobName,
// como o controller do Job faria no cluster real — mesma convenção de
// fixture usada por newRunnerTestComponent (runner_test.go).
func newJobPod(name, namespace, jobName string, phase corev1.PodPhase, createdAt time.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			Labels:            map[string]string{batchv1.JobNameLabel: jobName},
			CreationTimestamp: metav1.NewTime(createdAt),
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func TestGetPodStatus_ComponentNaoEncontrado(t *testing.T) {
	components, _ := newRunnerTestDB(t)
	provider := stubClusterProvider{clientset: fake.NewSimpleClientset()}

	_, err := GetPodStatus(context.Background(), provider, components, "id-inexistente")
	if !errors.Is(err, store.ErrComponentNotFound) {
		t.Fatalf("GetPodStatus() error = %v, esperava store.ErrComponentNotFound", err)
	}
}

func TestGetPodStatus_PodNaoEncontrado(t *testing.T) {
	components, _ := newRunnerTestDB(t)
	component := newRunnerTestComponent(t, components, "", "")
	provider := stubClusterProvider{clientset: fake.NewSimpleClientset()}

	_, err := GetPodStatus(context.Background(), provider, components, component.ID)
	if !errors.Is(err, ErrPodNotFound) {
		t.Fatalf("GetPodStatus() error = %v, esperava ErrPodNotFound", err)
	}
}

func TestGetPodStatus_DevolveOPodMaisRecente(t *testing.T) {
	components, _ := newRunnerTestDB(t)
	component := newRunnerTestComponent(t, components, "", "")

	older := newJobPod(component.ID+"-aaa", defaultNamespace, component.ID, corev1.PodFailed, time.Now().Add(-time.Hour))
	newer := newJobPod(component.ID+"-bbb", defaultNamespace, component.ID, corev1.PodRunning, time.Now())
	clientset := fake.NewSimpleClientset(older, newer)
	provider := stubClusterProvider{clientset: clientset}

	got, err := GetPodStatus(context.Background(), provider, components, component.ID)
	if err != nil {
		t.Fatalf("GetPodStatus() error = %v", err)
	}
	if got.PodName != newer.Name {
		t.Errorf("PodName = %q, esperava %q (pod mais recente)", got.PodName, newer.Name)
	}
	if got.Phase != string(corev1.PodRunning) {
		t.Errorf("Phase = %q, esperava %q", got.Phase, corev1.PodRunning)
	}
}

func TestGetPodStatus_ReflitaReasonEMessage(t *testing.T) {
	components, _ := newRunnerTestDB(t)
	component := newRunnerTestComponent(t, components, "", "")

	pod := newJobPod(component.ID+"-aaa", defaultNamespace, component.ID, corev1.PodFailed, time.Now())
	pod.Status.Reason = "Evicted"
	pod.Status.Message = "pod foi despejado por pressão de memória no node"
	clientset := fake.NewSimpleClientset(pod)
	provider := stubClusterProvider{clientset: clientset}

	got, err := GetPodStatus(context.Background(), provider, components, component.ID)
	if err != nil {
		t.Fatalf("GetPodStatus() error = %v", err)
	}
	if got.Phase != string(corev1.PodFailed) || got.Reason != "Evicted" || got.Message != pod.Status.Message {
		t.Errorf("GetPodStatus() = %+v, esperava Phase=Failed Reason=Evicted Message=%q", got, pod.Status.Message)
	}
}

func TestStreamPodLogs_NaoFollow_EscreveSnapshotUmaVez(t *testing.T) {
	components, _ := newRunnerTestDB(t)
	component := newRunnerTestComponent(t, components, "", "")

	pod := newJobPod(component.ID+"-aaa", defaultNamespace, component.ID, corev1.PodSucceeded, time.Now())
	clientset := fake.NewSimpleClientset(pod)
	provider := stubClusterProvider{clientset: clientset}

	var buf bytes.Buffer
	err := StreamPodLogs(context.Background(), provider, components, component.ID, &buf, false, nil, time.Millisecond)
	if err != nil {
		t.Fatalf("StreamPodLogs() error = %v", err)
	}
	if got := buf.String(); strings.Count(got, "fake logs") != 1 {
		t.Errorf("corpo dos logs = %q, esperava exatamente uma leitura", got)
	}
}

func TestStreamPodLogs_ComponentNaoEncontrado(t *testing.T) {
	components, _ := newRunnerTestDB(t)
	provider := stubClusterProvider{clientset: fake.NewSimpleClientset()}

	var buf bytes.Buffer
	err := StreamPodLogs(context.Background(), provider, components, "id-inexistente", &buf, false, nil, 0)
	if !errors.Is(err, store.ErrComponentNotFound) {
		t.Fatalf("StreamPodLogs() error = %v, esperava store.ErrComponentNotFound", err)
	}
	if buf.Len() != 0 {
		t.Errorf("buf = %q, esperava vazio (erro ocorreu antes de qualquer escrita)", buf.String())
	}
}

func TestStreamPodLogs_Follow_ParaQuandoPodFicaTerminal(t *testing.T) {
	components, _ := newRunnerTestDB(t)
	component := newRunnerTestComponent(t, components, "", "")

	pod := newJobPod(component.ID+"-aaa", defaultNamespace, component.ID, corev1.PodRunning, time.Now())
	clientset := fake.NewSimpleClientset(pod)

	// A segunda consulta de status (primeira dentro do loop de follow) já
	// devolve o Pod como Succeeded, garantindo que StreamPodLogs saia do
	// loop sem depender de um número fixo de iterações.
	getCount := 0
	clientset.PrependReactor("get", "pods", func(action ktesting.Action) (bool, k8sruntime.Object, error) {
		getCount++
		if getCount >= 1 {
			updated := pod.DeepCopy()
			updated.Status.Phase = corev1.PodSucceeded
			return true, updated, nil
		}
		return false, nil, nil
	})

	provider := stubClusterProvider{clientset: clientset}

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- StreamPodLogs(context.Background(), provider, components, component.ID, &buf, true, nil, time.Millisecond)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StreamPodLogs() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StreamPodLogs() não retornou após o pod ficar terminal")
	}

	if got := strings.Count(buf.String(), "fake logs"); got < 2 {
		t.Errorf("leituras de log = %d, esperava ao menos 2 (snapshot inicial + 1 poll)", got)
	}
}

func TestStreamPodLogs_Follow_RetornaQuandoContextoCancela(t *testing.T) {
	components, _ := newRunnerTestDB(t)
	component := newRunnerTestComponent(t, components, "", "")

	pod := newJobPod(component.ID+"-aaa", defaultNamespace, component.ID, corev1.PodRunning, time.Now())
	clientset := fake.NewSimpleClientset(pod)
	provider := stubClusterProvider{clientset: clientset}

	ctx, cancel := context.WithCancel(context.Background())

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- StreamPodLogs(ctx, provider, components, component.ID, &buf, true, nil, 50*time.Millisecond)
	}()

	// Dá tempo do snapshot inicial ser escrito antes de cancelar, garantindo
	// que o loop de follow (não só a checagem inicial) observa ctx.Done().
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StreamPodLogs() error = %v, esperava nil (desconexão do cliente não é erro)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StreamPodLogs() não retornou após o contexto ser cancelado")
	}
}
