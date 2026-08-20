package build

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// envSecretPrefix é o prefixo das env vars que armazenam tokens de acesso a
// repositórios privados, seguindo a mesma convenção KUBEFORGE_* usada pelo
// restante da configuração (cmd/kubeforge/main.go). Ver docs/adrs para a
// decisão de resolver credentialsSecretRef via env var local no MVP, em vez
// de um Kubernetes Secret.
const envSecretPrefix = "KUBEFORGE_GIT_TOKEN_"

// ErrCredentialsNotFound é retornado quando source.credentialsSecretRef está
// definido, mas nenhuma credencial correspondente foi encontrada.
var ErrCredentialsNotFound = errors.New("credenciais não encontradas para credentialsSecretRef")

// CredentialsResolver resolve um credentialsSecretRef (nome lógico) no token
// de acesso correspondente.
type CredentialsResolver interface {
	Resolve(secretRef string) (token string, err error)
}

// EnvCredentialsResolver resolve credentialsSecretRef lendo uma env var
// local, no formato KUBEFORGE_GIT_TOKEN_<SECRETREF_EM_MAIUSCULAS>. É a
// implementação padrão no MVP (perfil 100% local — docs/ARCHITECTURE.md §7),
// que não tem acesso a um cofre de segredos nem a Kubernetes Secrets a
// partir do binário, que roda fora do cluster.
type EnvCredentialsResolver struct{}

// Resolve implementa CredentialsResolver.
func (EnvCredentialsResolver) Resolve(secretRef string) (string, error) {
	key := envKeyFor(secretRef)
	token, ok := os.LookupEnv(key)
	if !ok || token == "" {
		return "", fmt.Errorf("%w: defina a env var %q com o token de acesso", ErrCredentialsNotFound, key)
	}
	return token, nil
}

func envKeyFor(secretRef string) string {
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - ('a' - 'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, secretRef)
	return envSecretPrefix + sanitized
}
