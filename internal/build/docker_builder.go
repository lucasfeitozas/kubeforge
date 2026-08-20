package build

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// BuildSpec descreve uma build de imagem a partir do resultado de um clone
// já realizado (ver Cloner), espelhando o bloco build do schema do
// Componente (docs/ARCHITECTURE.md §2.2).
type BuildSpec struct {
	// CloneResult é o resultado de um Cloner.Clone bem-sucedido: fornece o
	// commit SHA (usado na tag) e o caminho do Dockerfile validado.
	CloneResult CloneResult
	// ImageRepository é o repositório/nome da imagem (ex.:
	// "kubeforge/carga-cpu"), sem tag.
	ImageRepository string
	// ImageTagStrategy é a estratégia de tag (build.imageTagStrategy). Vazio
	// usa ImageTagStrategyCommitSHA, o default do MVP.
	ImageTagStrategy string
}

// BuildResult descreve o resultado de uma build de imagem.
type BuildResult struct {
	// ImageTag é a tag completa (repository:tag) gerada.
	ImageTag string
	// CommitSHA é o commit efetivamente buildado (mesmo valor de
	// BuildSpec.CloneResult.CommitSHA, repetido aqui por conveniência).
	CommitSHA string
	// Log é a saída combinada (stdout+stderr) do `docker build`.
	Log string
}

// Builder builda a imagem de um Componente a partir do código-fonte já
// clonado, tornando-a disponível ao cluster de destino.
type Builder interface {
	Build(ctx context.Context, spec BuildSpec) (*BuildResult, error)
}

// DockerBuilder é o Builder padrão do Build Broker no MVP (Opção A,
// docs/ARCHITECTURE.md §7.3): invoca o binário `docker` do host contra o
// daemon Docker interno do Minikube, injetando automaticamente as env vars
// equivalentes a `minikube docker-env` — sem depender do usuário rodar
// `eval` manualmente.
type DockerBuilder struct {
	// DockerEnv resolve as env vars do daemon Docker de destino. nil usa
	// MinikubeDockerEnv{}.
	DockerEnv DockerEnvResolver
}

// NewDockerBuilder cria um DockerBuilder com o DockerEnvResolver padrão.
func NewDockerBuilder() *DockerBuilder {
	return &DockerBuilder{DockerEnv: MinikubeDockerEnv{}}
}

// Build implementa Builder.
func (b *DockerBuilder) Build(ctx context.Context, spec BuildSpec) (*BuildResult, error) {
	if spec.ImageRepository == "" {
		return nil, errors.New("imageRepository é obrigatório")
	}

	tag, err := BuildImageTag(spec.ImageRepository, spec.ImageTagStrategy, spec.CloneResult.CommitSHA)
	if err != nil {
		return nil, err
	}

	resolver := b.DockerEnv
	if resolver == nil {
		resolver = MinikubeDockerEnv{}
	}
	dockerEnv, err := resolver.Resolve(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolvendo env vars do docker-env: %w", err)
	}

	buildContext := filepath.Dir(spec.CloneResult.DockerfilePath)
	cmd := exec.CommandContext(ctx, "docker", "build",
		"-t", tag,
		"-f", spec.CloneResult.DockerfilePath,
		buildContext,
	)
	cmd.Env = mergeEnv(os.Environ(), dockerEnv)

	var log bytes.Buffer
	cmd.Stdout = &log
	cmd.Stderr = &log

	if err := cmd.Run(); err != nil {
		return &BuildResult{Log: log.String()}, fmt.Errorf("docker build falhou: %w", err)
	}

	return &BuildResult{
		ImageTag:  tag,
		CommitSHA: spec.CloneResult.CommitSHA,
		Log:       log.String(),
	}, nil
}

// mergeEnv combina base (tipicamente os.Environ()) com overrides, onde
// overrides tem precedência para chaves em comum. exec.Cmd usa a última
// ocorrência de uma chave em Env, então basta anexar overrides ao final —
// sem necessidade de filtrar duplicatas de base manualmente.
func mergeEnv(base []string, overrides map[string]string) []string {
	merged := make([]string, 0, len(base)+len(overrides))
	merged = append(merged, base...)
	for k, v := range overrides {
		merged = append(merged, k+"="+v)
	}
	return merged
}
