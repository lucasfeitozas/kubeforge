package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// timeLayout casa com o formato gerado por strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
// nas migrations, para que valores lidos e escritos por Go sejam consistentes
// com os defaults aplicados diretamente pelo SQLite.
const timeLayout = "2006-01-02T15:04:05.000Z"

// Component é a representação em memória de uma linha da tabela components.
// Os blocos aninhados do schema JSON da seção 2.2 da arquitetura (source,
// build, resources, runtime, hooks, target_context, lifecycle) são mantidos
// como JSON cru: o repository apenas move esses bytes entre Go e SQLite, sem
// interpretar sua estrutura interna.
type Component struct {
	ID            string
	Nome          string
	Descricao     string
	Source        json.RawMessage
	Build         json.RawMessage
	Resources     json.RawMessage
	Runtime       json.RawMessage
	Hooks         json.RawMessage
	TargetContext json.RawMessage
	Lifecycle     json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ErrComponentNotFound é retornado por Get, Update e Delete quando nenhum
// componente com o id informado existe.
var ErrComponentNotFound = errors.New("componente não encontrado")

// ComponentRepository persiste Componentes, isolando a camada de API do
// acesso direto ao SQL do Metadata Store.
type ComponentRepository interface {
	// Create insere um novo componente, preenchendo ID, CreatedAt e UpdatedAt.
	Create(ctx context.Context, c *Component) error
	// Get busca um componente pelo id. Retorna ErrComponentNotFound se não existir.
	Get(ctx context.Context, id string) (*Component, error)
	// List retorna todos os componentes, ordenados por data de criação.
	List(ctx context.Context) ([]*Component, error)
	// Update substitui todos os campos de um componente existente e atualiza
	// UpdatedAt. Retorna ErrComponentNotFound se o id não existir.
	Update(ctx context.Context, c *Component) error
	// Delete remove um componente pelo id. Retorna ErrComponentNotFound se não
	// existir. Falha se houver execuções referenciando o componente (ON DELETE
	// RESTRICT).
	Delete(ctx context.Context, id string) error
}
