package controller

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/lucasfeitozas/kubeforge/internal/k8s"
	"github.com/lucasfeitozas/kubeforge/internal/store"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// managedLabelSelector seleciona todo recurso criado pelo Controller
// (E5.S3): kubeforge.io/managed=true.
const managedLabelSelector = managedLabelKey + "=" + managedLabelValue

// CleanupResult descreve um recurso removido por RunCleanup, usado tanto
// para montar a resposta do endpoint HTTP quanto para persistir o log de
// auditoria (E5.S2, AC2/AC3).
type CleanupResult struct {
	Kind      string
	Name      string
	Namespace string
}

// RunCleanup remove todo Job, Pod e PersistentVolumeClaim rotulado
// kubeforge.io/managed=true no namespace informado (E5.S2, AC1). Ordem:
// Jobs primeiro — a exclusão em cascata do Kubernetes (propagação
// Background) já remove os Pods que eles possuem, então tentar removê-los
// de novo a seguir só encontra NotFound, tolerado — depois os Pods
// remanescentes (órfãos ou de execuções sem Job dono), depois PVCs.
//
// Best-effort: um erro no meio interrompe a varredura, mas os resultados já
// coletados são retornados junto do erro, para que o chamador ainda
// registre a auditoria do que foi removido até ali.
func RunCleanup(ctx context.Context, provider k8s.ClusterProvider, clusterKey, namespace string) ([]CleanupResult, error) {
	return runCleanupWithSelector(ctx, provider, clusterKey, namespace, managedLabelSelector)
}

// RunComponentCleanup remove os recursos (Job principal, Job de
// verificação, Pods e PersistentVolumeClaim) do Componente componentID —
// não o namespace inteiro (E6.S3, AC2: "cleanup remove os recursos da
// execução mais recente"). Como Job/PVC têm nomes determinísticos por
// componentID (ver BuildJob/BuildPostRunJob/buildStoragePVC em
// internal/controller/job_builder.go), só existe uma geração de recursos
// rotulados kubeforge.io/component=<id> viva no cluster a qualquer
// momento: filtrar por esse label já escopa exatamente para a execução
// mais recente, sem precisar consultar a tabela executions (ver ADR 0015).
//
// Retorna store.ErrComponentNotFound se componentID não existir.
func RunComponentCleanup(ctx context.Context, provider k8s.ClusterProvider, components store.ComponentRepository, componentID string) ([]CleanupResult, error) {
	component, err := components.Get(ctx, componentID)
	if err != nil {
		return nil, err
	}

	tc, err := parseTargetContext(component.TargetContext)
	if err != nil {
		return nil, fmt.Errorf("interpretando targetContext do componente %q: %w", componentID, err)
	}

	selector := managedLabelSelector + "," + componentLabelKey + "=" + componentID
	return runCleanupWithSelector(ctx, provider, tc.Cluster, tc.Namespace, selector)
}

// runCleanupWithSelector implementa RunCleanup/RunComponentCleanup,
// parametrizada pelo label selector usado para listar Jobs/Pods/PVCs.
func runCleanupWithSelector(ctx context.Context, provider k8s.ClusterProvider, clusterKey, namespace, labelSelector string) ([]CleanupResult, error) {
	slog.Info("cleanup iniciado", "cluster", clusterKey, "namespace", namespace, "selector", labelSelector)

	clientset, err := provider.GetClientset(ctx, clusterKey)
	if err != nil {
		slog.Error("cleanup falhou", "cluster", clusterKey, "namespace", namespace, "error", err)
		return nil, fmt.Errorf("obtendo clientset do cluster %q: %w", clusterKey, err)
	}

	var results []CleanupResult
	listOpts := metav1.ListOptions{LabelSelector: labelSelector}

	jobs, err := clientset.BatchV1().Jobs(namespace).List(ctx, listOpts)
	if err != nil {
		slog.Error("cleanup falhou", "namespace", namespace, "error", err)
		return results, fmt.Errorf("listando Jobs rotulados no namespace %q: %w", namespace, err)
	}
	background := metav1.DeletePropagationBackground
	for _, job := range jobs.Items {
		if err := clientset.BatchV1().Jobs(namespace).Delete(ctx, job.Name, metav1.DeleteOptions{PropagationPolicy: &background}); err != nil {
			slog.Error("cleanup falhou", "namespace", namespace, "error", err)
			return results, fmt.Errorf("removendo Job %q no namespace %q: %w", job.Name, namespace, err)
		}
		slog.Info("recurso removido", "kind", "Job", "name", job.Name, "namespace", namespace)
		results = append(results, CleanupResult{Kind: "Job", Name: job.Name, Namespace: namespace})
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, listOpts)
	if err != nil {
		slog.Error("cleanup falhou", "namespace", namespace, "error", err)
		return results, fmt.Errorf("listando Pods rotulados no namespace %q: %w", namespace, err)
	}
	for _, pod := range pods.Items {
		if err := clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			slog.Error("cleanup falhou", "namespace", namespace, "error", err)
			return results, fmt.Errorf("removendo Pod %q no namespace %q: %w", pod.Name, namespace, err)
		}
		slog.Info("recurso removido", "kind", "Pod", "name", pod.Name, "namespace", namespace)
		results = append(results, CleanupResult{Kind: "Pod", Name: pod.Name, Namespace: namespace})
	}

	pvcs, err := clientset.CoreV1().PersistentVolumeClaims(namespace).List(ctx, listOpts)
	if err != nil {
		slog.Error("cleanup falhou", "namespace", namespace, "error", err)
		return results, fmt.Errorf("listando PersistentVolumeClaims rotuladas no namespace %q: %w", namespace, err)
	}
	for _, pvc := range pvcs.Items {
		if err := clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvc.Name, metav1.DeleteOptions{}); err != nil {
			slog.Error("cleanup falhou", "namespace", namespace, "error", err)
			return results, fmt.Errorf("removendo PersistentVolumeClaim %q no namespace %q: %w", pvc.Name, namespace, err)
		}
		slog.Info("recurso removido", "kind", "PersistentVolumeClaim", "name", pvc.Name, "namespace", namespace)
		results = append(results, CleanupResult{Kind: "PersistentVolumeClaim", Name: pvc.Name, Namespace: namespace})
	}

	slog.Info("cleanup concluído", "namespace", namespace, "recursos_removidos", len(results))
	return results, nil
}

// PersistCleanupAudit grava em audit cada CleanupResult retornado por
// RunCleanup (E5.S2, AC3 — log de auditoria simples do que foi removido e
// quando).
func PersistCleanupAudit(ctx context.Context, audit store.CleanupAuditRepository, results []CleanupResult) error {
	for _, res := range results {
		entry := &store.CleanupAuditEntry{
			ResourceKind: res.Kind,
			ResourceName: res.Name,
			Namespace:    res.Namespace,
		}
		if err := audit.Record(ctx, entry); err != nil {
			return fmt.Errorf("registrando auditoria de %s %q: %w", res.Kind, res.Name, err)
		}
	}
	return nil
}
