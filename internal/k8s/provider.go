package k8s

import (
	"context"
	"errors"

	"k8s.io/client-go/kubernetes"
)

// MinikubeClusterKey é a chave lógica do único cluster-alvo suportado no MVP.
// Também é usado como nome do contexto esperado no kubeconfig.
const MinikubeClusterKey = "minikube"

// ClusterProvider abstrai a obtenção do clientset para um cluster-alvo,
// mantendo a assinatura genérica descrita em docs/ARCHITECTURE.md (seção 3.2)
// para permitir, no futuro, um provider "eks" sem alterar os consumidores.
// No MVP existe um único provider registrado: MinikubeProvider.
type ClusterProvider interface {
	GetClientset(ctx context.Context, clusterKey string) (kubernetes.Interface, error)
}

var (
	// ErrKubeconfigNotFound indica que o arquivo de kubeconfig não existe no caminho informado.
	ErrKubeconfigNotFound = errors.New("kubeconfig não encontrado")
	// ErrContextNaoAtivo indica que o contexto esperado (minikube) não é o contexto
	// corrente do kubeconfig, ou não está definido nele.
	ErrContextNaoAtivo = errors.New("contexto minikube não está ativo no kubeconfig")
)
