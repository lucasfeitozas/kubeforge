package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type sqliteComponentRepository struct {
	db *sql.DB
}

// NewComponentRepository cria um ComponentRepository apoiado em db, que já
// deve ter as migrations aplicadas (ver Open e Migrate).
func NewComponentRepository(db *sql.DB) ComponentRepository {
	return &sqliteComponentRepository{db: db}
}

func (r *sqliteComponentRepository) Create(ctx context.Context, c *Component) error {
	if err := c.Validate(); err != nil {
		return err
	}

	id := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO components (id, nome, descricao, source, build, resources, runtime, hooks, target_context, lifecycle, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		id, c.Nome, nullString(c.Descricao),
		string(c.Source), nullJSON(c.Build), string(c.Resources), string(c.Runtime),
		nullJSON(c.Hooks), string(c.TargetContext), nullJSON(c.Lifecycle),
		now.Format(timeLayout), now.Format(timeLayout),
	)
	if err != nil {
		return fmt.Errorf("criando componente: %w", err)
	}

	c.ID = id
	c.CreatedAt = now
	c.UpdatedAt = now
	return nil
}

func (r *sqliteComponentRepository) Get(ctx context.Context, id string) (*Component, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, nome, descricao, source, build, resources, runtime, hooks, target_context, lifecycle, created_at, updated_at
		FROM components WHERE id = ?
	`, id)

	c, err := scanComponent(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrComponentNotFound
		}
		return nil, fmt.Errorf("buscando componente %q: %w", id, err)
	}
	return c, nil
}

func (r *sqliteComponentRepository) List(ctx context.Context) ([]*Component, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, nome, descricao, source, build, resources, runtime, hooks, target_context, lifecycle, created_at, updated_at
		FROM components ORDER BY created_at, rowid
	`)
	if err != nil {
		return nil, fmt.Errorf("listando componentes: %w", err)
	}
	defer rows.Close()

	components := []*Component{}
	for rows.Next() {
		c, err := scanComponent(rows)
		if err != nil {
			return nil, fmt.Errorf("lendo componente: %w", err)
		}
		components = append(components, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listando componentes: %w", err)
	}
	return components, nil
}

func (r *sqliteComponentRepository) Update(ctx context.Context, c *Component) error {
	if err := c.Validate(); err != nil {
		return err
	}

	now := time.Now().UTC().Truncate(time.Millisecond)

	result, err := r.db.ExecContext(ctx, `
		UPDATE components
		SET nome = ?, descricao = ?, source = ?, build = ?, resources = ?, runtime = ?, hooks = ?, target_context = ?, lifecycle = ?, updated_at = ?
		WHERE id = ?
	`,
		c.Nome, nullString(c.Descricao),
		string(c.Source), nullJSON(c.Build), string(c.Resources), string(c.Runtime),
		nullJSON(c.Hooks), string(c.TargetContext), nullJSON(c.Lifecycle),
		now.Format(timeLayout), c.ID,
	)
	if err != nil {
		return fmt.Errorf("atualizando componente %q: %w", c.ID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("atualizando componente %q: %w", c.ID, err)
	}
	if affected == 0 {
		return ErrComponentNotFound
	}

	c.UpdatedAt = now
	return nil
}

func (r *sqliteComponentRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM components WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("removendo componente %q: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("removendo componente %q: %w", id, err)
	}
	if affected == 0 {
		return ErrComponentNotFound
	}
	return nil
}

// rowScanner é satisfeito tanto por *sql.Row quanto por *sql.Rows, permitindo
// reusar a lógica de scan entre Get e List.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanComponent(row rowScanner) (*Component, error) {
	var (
		c                                  Component
		descricao, build, hooks, lifecycle sql.NullString
		source, resources, runtime, target string
		createdAt, updatedAt               string
	)

	if err := row.Scan(
		&c.ID, &c.Nome, &descricao,
		&source, &build, &resources, &runtime,
		&hooks, &target, &lifecycle,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}

	c.Descricao = descricao.String
	c.Source = json.RawMessage(source)
	c.Build = nullableRawMessage(build)
	c.Resources = json.RawMessage(resources)
	c.Runtime = json.RawMessage(runtime)
	c.Hooks = nullableRawMessage(hooks)
	c.TargetContext = json.RawMessage(target)
	c.Lifecycle = nullableRawMessage(lifecycle)

	parsedCreatedAt, err := time.Parse(timeLayout, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parseando created_at: %w", err)
	}
	parsedUpdatedAt, err := time.Parse(timeLayout, updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parseando updated_at: %w", err)
	}
	c.CreatedAt = parsedCreatedAt
	c.UpdatedAt = parsedUpdatedAt

	return &c, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func nullableRawMessage(s sql.NullString) json.RawMessage {
	if !s.Valid {
		return nil
	}
	return json.RawMessage(s.String)
}
