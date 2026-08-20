package store

import (
	"context"
	"errors"
	"time"
)

// Execution é a representação em memória de uma linha da tabela executions:
// uma execução de build/deploy de um Componente.
type Execution struct {
	ID          string
	ComponentID string
	// Phase reflete o CHECK da tabela: Pending, Running, Succeeded ou
	// Failed. A máquina de estados completa (transições, mensagens de
	// falha) é escopo de E3.S3 — este repositório só preenche o default
	// 'Pending' na criação e não altera Phase depois.
	Phase       string
	StartedAt   *time.Time
	CompletedAt *time.Time
	ImageDigest string
	// ImageTag é a tag de imagem gerada pelo Build Broker (ver
	// internal/build.BuildImageTag), seguindo build.imageTagStrategy.
	ImageTag string
	// BuildLog é a saída combinada (stdout+stderr) do `docker build`
	// associada a esta execution (ver internal/build.BuildResult.Log).
	BuildLog  string
	CreatedAt time.Time
}

// ErrExecutionNotFound é retornado por Get e UpdateBuildLog quando nenhuma
// execution com o id informado existe.
var ErrExecutionNotFound = errors.New("execution não encontrada")

// ExecutionRepository persiste Executions, isolando a camada de build/API do
// acesso direto ao SQL do Metadata Store.
type ExecutionRepository interface {
	// Create insere uma nova execution, preenchendo ID, Phase ('Pending')
	// e CreatedAt.
	Create(ctx context.Context, e *Execution) error
	// Get busca uma execution pelo id. Retorna ErrExecutionNotFound se não existir.
	Get(ctx context.Context, id string) (*Execution, error)
	// List retorna as executions de um componente, ordenadas por data de criação.
	List(ctx context.Context, componentID string) ([]*Execution, error)
	// UpdateBuildLog persiste a tag de imagem e o log resultantes de um
	// docker build, associando-os à execution correspondente. Retorna
	// ErrExecutionNotFound se o id não existir.
	UpdateBuildLog(ctx context.Context, id, imageTag, buildLog string) error
}
