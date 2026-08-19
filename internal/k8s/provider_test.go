package k8s

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const validKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
    insecure-skip-tls-verify: true
  name: minikube
contexts:
- context:
    cluster: minikube
    user: minikube
  name: minikube
current-context: minikube
users:
- name: minikube
  user:
    token: fake-token
`

const wrongContextKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
  name: docker-desktop
contexts:
- context:
    cluster: docker-desktop
    user: docker-desktop
  name: docker-desktop
current-context: docker-desktop
users:
- name: docker-desktop
  user:
    token: fake-token
`

func TestMinikubeProvider_GetClientset(t *testing.T) {
	tests := []struct {
		name       string
		content    string // "" = não cria o arquivo
		clusterKey string
		wantErr    error // checado via errors.Is; nil = sem erro esperado
	}{
		{
			name:       "kubeconfig ausente",
			content:    "",
			clusterKey: MinikubeClusterKey,
			wantErr:    ErrKubeconfigNotFound,
		},
		{
			name:       "contexto ativo diferente de minikube",
			content:    wrongContextKubeconfig,
			clusterKey: MinikubeClusterKey,
			wantErr:    ErrContextNaoAtivo,
		},
		{
			name:       "kubeconfig válido com contexto minikube ativo",
			content:    validKubeconfig,
			clusterKey: MinikubeClusterKey,
			wantErr:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config")
			if tt.content != "" {
				if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
					t.Fatalf("erro ao preparar kubeconfig de teste: %v", err)
				}
			}

			provider := NewMinikubeProvider(path)
			clientset, err := provider.GetClientset(context.Background(), tt.clusterKey)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("erro = %v, esperava wrap de %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if clientset == nil {
				t.Fatal("esperava clientset não-nulo")
			}
		})
	}
}

func TestMinikubeProvider_GetClientset_ClusterKeyNaoSuportada(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(validKubeconfig), 0o600); err != nil {
		t.Fatalf("erro ao preparar kubeconfig de teste: %v", err)
	}

	provider := NewMinikubeProvider(path)
	if _, err := provider.GetClientset(context.Background(), "eks"); err == nil {
		t.Fatal("esperava erro para clusterKey não suportada, obteve nil")
	}
}
