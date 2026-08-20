package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const executionPhasePending = "Pending"

type sqliteExecutionRepository struct {
	db *sql.DB
}

// NewExecutionRepository cria um ExecutionRepository apoiado em db, que já
// deve ter as migrations aplicadas (ver Open e Migrate).
func NewExecutionRepository(db *sql.DB) ExecutionRepository {
	return &sqliteExecutionRepository{db: db}
}

func (r *sqliteExecutionRepository) Create(ctx context.Context, e *Execution) error {
	id := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO executions (id, component_id, phase, created_at)
		VALUES (?, ?, ?, ?)
	`,
		id, e.ComponentID, executionPhasePending, now.Format(timeLayout),
	)
	if err != nil {
		return fmt.Errorf("criando execution: %w", err)
	}

	e.ID = id
	e.Phase = executionPhasePending
	e.CreatedAt = now
	return nil
}

func (r *sqliteExecutionRepository) Get(ctx context.Context, id string) (*Execution, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, component_id, phase, started_at, completed_at, image_digest, image_tag, build_log, created_at
		FROM executions WHERE id = ?
	`, id)

	e, err := scanExecution(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrExecutionNotFound
		}
		return nil, fmt.Errorf("buscando execution %q: %w", id, err)
	}
	return e, nil
}

func (r *sqliteExecutionRepository) List(ctx context.Context, componentID string) ([]*Execution, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, component_id, phase, started_at, completed_at, image_digest, image_tag, build_log, created_at
		FROM executions WHERE component_id = ? ORDER BY created_at, rowid
	`, componentID)
	if err != nil {
		return nil, fmt.Errorf("listando executions do componente %q: %w", componentID, err)
	}
	defer rows.Close()

	executions := []*Execution{}
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, fmt.Errorf("lendo execution: %w", err)
		}
		executions = append(executions, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listando executions do componente %q: %w", componentID, err)
	}
	return executions, nil
}

func (r *sqliteExecutionRepository) UpdateBuildLog(ctx context.Context, id, imageTag, buildLog string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE executions SET image_tag = ?, build_log = ? WHERE id = ?
	`, nullString(imageTag), nullString(buildLog), id)
	if err != nil {
		return fmt.Errorf("atualizando log de build da execution %q: %w", id, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("atualizando log de build da execution %q: %w", id, err)
	}
	if affected == 0 {
		return ErrExecutionNotFound
	}
	return nil
}

func scanExecution(row rowScanner) (*Execution, error) {
	var (
		e                               Execution
		startedAt, completedAt          sql.NullString
		imageDigest, imageTag, buildLog sql.NullString
		createdAt                       string
	)

	if err := row.Scan(
		&e.ID, &e.ComponentID, &e.Phase,
		&startedAt, &completedAt,
		&imageDigest, &imageTag, &buildLog,
		&createdAt,
	); err != nil {
		return nil, err
	}

	if startedAt.Valid {
		t, err := time.Parse(timeLayout, startedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parseando started_at: %w", err)
		}
		e.StartedAt = &t
	}
	if completedAt.Valid {
		t, err := time.Parse(timeLayout, completedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parseando completed_at: %w", err)
		}
		e.CompletedAt = &t
	}
	e.ImageDigest = imageDigest.String
	e.ImageTag = imageTag.String
	e.BuildLog = buildLog.String

	parsedCreatedAt, err := time.Parse(timeLayout, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parseando created_at: %w", err)
	}
	e.CreatedAt = parsedCreatedAt

	return &e, nil
}
