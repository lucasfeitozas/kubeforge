package k8s

import (
	"context"
	"fmt"
	"os"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// serverVersionTimeout limita o tempo de qualquer chamada feita através do
// clientset construído aqui (inclui o ServerVersion() usado no startup),
// garantindo o comportamento de "fail fast" exigido pela história E1.S3.
const serverVersionTimeout = 5 * time.Second

// MinikubeProvider é o único ClusterProvider registrado no MVP.
type MinikubeProvider struct {
	kubeconfigPath  string
	expectedContext string
}

// NewMinikubeProvider cria o provider apontando para o kubeconfig informado
// (tipicamente cfg.kubeconfig, já resolvido via --kubeconfig/KUBEFORGE_KUBECONFIG
// ou o default ~/.kube/config).
func NewMinikubeProvider(kubeconfigPath string) *MinikubeProvider {
	return &MinikubeProvider{
		kubeconfigPath:  kubeconfigPath,
		expectedContext: MinikubeClusterKey,
	}
}

// GetClientset implementa ClusterProvider. Só aceita clusterKey ==
// MinikubeClusterKey (único provider registrado no MVP).
func (p *MinikubeProvider) GetClientset(ctx context.Context, clusterKey string) (kubernetes.Interface, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if clusterKey != MinikubeClusterKey {
		return nil, fmt.Errorf("cluster provider minikube não suporta a chave %q (único provider registrado: %q)", clusterKey, MinikubeClusterKey)
	}

	if _, err := os.Stat(p.kubeconfigPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w em %q: rode `minikube start` (que gera o kubeconfig padrão) ou informe um caminho válido via --kubeconfig / KUBEFORGE_KUBECONFIG", ErrKubeconfigNotFound, p.kubeconfigPath)
		}
		return nil, fmt.Errorf("erro ao acessar kubeconfig em %q: %w", p.kubeconfigPath, err)
	}

	rawConfig, err := clientcmd.LoadFromFile(p.kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("erro ao carregar kubeconfig em %q: %w", p.kubeconfigPath, err)
	}

	current := rawConfig.CurrentContext
	if current == "" {
		current = "(nenhum)"
	}
	if rawConfig.CurrentContext != p.expectedContext {
		return nil, fmt.Errorf("%w: contexto atual é %q, esperado %q — rode `minikube start` ou `kubectl config use-context %s`", ErrContextNaoAtivo, current, p.expectedContext, p.expectedContext)
	}
	if _, ok := rawConfig.Contexts[p.expectedContext]; !ok {
		return nil, fmt.Errorf("%w: contexto %q não está definido em %q — rode `minikube start` para gerar/atualizar o kubeconfig do Minikube", ErrContextNaoAtivo, p.expectedContext, p.kubeconfigPath)
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", p.kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("erro ao construir configuração do cliente k8s a partir de %q: %w", p.kubeconfigPath, err)
	}
	restConfig.Timeout = serverVersionTimeout

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar clientset k8s: %w", err)
	}
	return clientset, nil
}
