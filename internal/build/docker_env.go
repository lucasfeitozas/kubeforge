package build

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// defaultMinikubeProfile é o profile usado quando MinikubeDockerEnv.Profile
// está vazio. O MVP só suporta um único cluster (targetContext.cluster:
// minikube fixo, docs/ARCHITECTURE.md §7.2), então o profile padrão do
// Minikube já cobre o caso de uso real.
const defaultMinikubeProfile = "minikube"

// DockerEnvResolver resolve as variáveis de ambiente necessárias para que um
// `docker build` local seja executado contra o daemon Docker interno do
// Minikube, equivalentes ao que `eval $(minikube docker-env)` exportaria em
// um shell interativo (docs/ARCHITECTURE.md §7.3, Opção A).
type DockerEnvResolver interface {
	Resolve(ctx context.Context) (map[string]string, error)
}

// MinikubeDockerEnv é o DockerEnvResolver padrão do Build Broker: invoca o
// binário `minikube` do host, mesma abordagem de GitCloner para `git` (ver
// docs/adrs/0001-clonagem-repositorio-git-cli.md) — zero dependências novas
// no go.mod, comportamento idêntico ao uso manual da CLI.
type MinikubeDockerEnv struct {
	// Profile é o profile do Minikube a consultar. Vazio usa
	// defaultMinikubeProfile ("minikube").
	Profile string
}

// Resolve implementa DockerEnvResolver.
func (m MinikubeDockerEnv) Resolve(ctx context.Context) (map[string]string, error) {
	profile := m.Profile
	if profile == "" {
		profile = defaultMinikubeProfile
	}

	cmd := exec.CommandContext(ctx, "minikube", "-p", profile, "docker-env", "--shell", "none")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("minikube docker-env (profile %q) falhou: %w", profile, err)
		}
		return nil, fmt.Errorf("minikube docker-env (profile %q) falhou: %w: %s", profile, err, msg)
	}

	return parseDockerEnv(stdout.String()), nil
}

// parseDockerEnv interpreta a saída de `minikube docker-env --shell none`:
// uma linha CHAVE=VALOR por variável, sem `export`, aspas ou comentários —
// formato pensado justamente para consumo por scripts/processos, em vez do
// shell interativo.
func parseDockerEnv(output string) map[string]string {
	env := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			continue
		}
		env[key] = strings.Trim(value, `"`)
	}
	return env
}
