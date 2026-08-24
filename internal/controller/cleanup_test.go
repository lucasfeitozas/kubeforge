package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/lucasfeitozas/kubeforge/internal/store"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func managedObjectMeta(name, namespace string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
		Labels:    map[string]string{managedLabelKey: managedLabelValue, componentLabelKey: name},
	}
}

func TestRunCleanup_RemoveApenasRecursosRotulados(t *testing.T) {
	ns := "default"
	clientset := fake.NewSimpleClientset(
		&batchv1.Job{ObjectMeta: managedObjectMeta("job-rotulado", ns)},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "job-sem-label", Namespace: ns}},
		&corev1.Pod{ObjectMeta: managedObjectMeta("pod-rotulado", ns)},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-sem-label", Namespace: ns}},
		&corev1.PersistentVolumeClaim{ObjectMeta: managedObjectMeta("pvc-rotulado", ns)},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc-sem-label", Namespace: ns}},
	)

	results, err := RunCleanup(context.Background(), stubClusterProvider{clientset: clientset}, "minikube", ns)
	if err != nil {
		t.Fatalf("RunCleanup() error = %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("RunCleanup() retornou %d resultados, esperava 3: %+v", len(results), results)
	}
	want := map[string]bool{"Job:job-rotulado": false, "Pod:pod-rotulado": false, "PersistentVolumeClaim:pvc-rotulado": false}
	for _, r := range results {
		key := r.Kind + ":" + r.Name
		if _, ok := want[key]; !ok {
			t.Errorf("resultado inesperado: %+v", r)
			continue
		}
		want[key] = true
		if r.Namespace != ns {
			t.Errorf("resultado %+v tem namespace %q, esperava %q", r, r.Namespace, ns)
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("esperava resultado para %q, não encontrado", key)
		}
	}

	assertGone(t, func() error {
		_, err := clientset.BatchV1().Jobs(ns).Get(context.Background(), "job-rotulado", metav1.GetOptions{})
		return err
	})
	assertGone(t, func() error {
		_, err := clientset.CoreV1().Pods(ns).Get(context.Background(), "pod-rotulado", metav1.GetOptions{})
		return err
	})
	assertGone(t, func() error {
		_, err := clientset.CoreV1().PersistentVolumeClaims(ns).Get(context.Background(), "pvc-rotulado", metav1.GetOptions{})
		return err
	})

	assertPresent(t, func() error {
		_, err := clientset.BatchV1().Jobs(ns).Get(context.Background(), "job-sem-label", metav1.GetOptions{})
		return err
	})
	assertPresent(t, func() error {
		_, err := clientset.CoreV1().Pods(ns).Get(context.Background(), "pod-sem-label", metav1.GetOptions{})
		return err
	})
	assertPresent(t, func() error {
		_, err := clientset.CoreV1().PersistentVolumeClaims(ns).Get(context.Background(), "pvc-sem-label", metav1.GetOptions{})
		return err
	})
}

func TestRunCleanup_SemRecursosRotuladosNaoRetornaErro(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	results, err := RunCleanup(context.Background(), stubClusterProvider{clientset: clientset}, "minikube", "default")
	if err != nil {
		t.Fatalf("RunCleanup() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("RunCleanup() retornou %d resultados, esperava 0", len(results))
	}
}

func TestRunCleanup_ErroDeClusterPropaga(t *testing.T) {
	wantErr := errors.New("cluster indisponível")
	_, err := RunCleanup(context.Background(), stubClusterProvider{err: wantErr}, "minikube", "default")
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunCleanup() error = %v, esperava envolver %v", err, wantErr)
	}
}

func TestRunCleanup_IsolaPorNamespace(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&batchv1.Job{ObjectMeta: managedObjectMeta("job-default", "default")},
		&batchv1.Job{ObjectMeta: managedObjectMeta("job-outro-ns", "outro-namespace")},
	)

	results, err := RunCleanup(context.Background(), stubClusterProvider{clientset: clientset}, "minikube", "default")
	if err != nil {
		t.Fatalf("RunCleanup() error = %v", err)
	}
	if len(results) != 1 || results[0].Name != "job-default" {
		t.Fatalf("RunCleanup() = %+v, esperava só job-default", results)
	}

	assertPresent(t, func() error {
		_, err := clientset.BatchV1().Jobs("outro-namespace").Get(context.Background(), "job-outro-ns", metav1.GetOptions{})
		return err
	})
}

func assertGone(t *testing.T, get func() error) {
	t.Helper()
	if err := get(); !apierrors.IsNotFound(err) {
		t.Fatalf("esperava recurso removido (NotFound), get() error = %v", err)
	}
}

func assertPresent(t *testing.T, get func() error) {
	t.Helper()
	if err := get(); err != nil {
		t.Fatalf("esperava recurso presente, get() error = %v", err)
	}
}

// stubCleanupAuditRepository coleta as entradas registradas em memória,
// evitando a necessidade de um SQLite real nos testes de
// PersistCleanupAudit.
type stubCleanupAuditRepository struct {
	entries []*store.CleanupAuditEntry
	err     error
}

func (s *stubCleanupAuditRepository) Record(ctx context.Context, entry *store.CleanupAuditEntry) error {
	if s.err != nil {
		return s.err
	}
	s.entries = append(s.entries, entry)
	return nil
}

func TestPersistCleanupAudit_RegistraTodosOsResultados(t *testing.T) {
	audit := &stubCleanupAuditRepository{}
	results := []CleanupResult{
		{Kind: "Job", Name: "componente-teste", Namespace: "default"},
		{Kind: "Pod", Name: "componente-teste-abc", Namespace: "default"},
	}

	if err := PersistCleanupAudit(context.Background(), audit, results); err != nil {
		t.Fatalf("PersistCleanupAudit() error = %v", err)
	}
	if len(audit.entries) != 2 {
		t.Fatalf("PersistCleanupAudit() registrou %d entradas, esperava 2", len(audit.entries))
	}
	if audit.entries[0].ResourceKind != "Job" || audit.entries[0].ResourceName != "componente-teste" {
		t.Errorf("entries[0] = %+v, esperava Kind=Job Name=componente-teste", audit.entries[0])
	}
}

func TestPersistCleanupAudit_ErroDeRepositorioPropaga(t *testing.T) {
	wantErr := errors.New("falha ao gravar no banco")
	audit := &stubCleanupAuditRepository{err: wantErr}
	results := []CleanupResult{{Kind: "Job", Name: "componente-teste", Namespace: "default"}}

	err := PersistCleanupAudit(context.Background(), audit, results)
	if !errors.Is(err, wantErr) {
		t.Fatalf("PersistCleanupAudit() error = %v, esperava envolver %v", err, wantErr)
	}
}
