package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type sqliteCleanupAuditRepository struct {
	db *sql.DB
}

// NewCleanupAuditRepository cria um CleanupAuditRepository apoiado em db,
// que já deve ter as migrations aplicadas (ver Open e Migrate).
func NewCleanupAuditRepository(db *sql.DB) CleanupAuditRepository {
	return &sqliteCleanupAuditRepository{db: db}
}

func (r *sqliteCleanupAuditRepository) Record(ctx context.Context, entry *CleanupAuditEntry) error {
	id := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO cleanup_audit_log (id, resource_kind, resource_name, namespace, removed_at)
		VALUES (?, ?, ?, ?, ?)
	`,
		id, entry.ResourceKind, entry.ResourceName, entry.Namespace, now.Format(timeLayout),
	)
	if err != nil {
		return fmt.Errorf("registrando remoção de %s %q no log de auditoria: %w", entry.ResourceKind, entry.ResourceName, err)
	}

	entry.ID = id
	entry.RemovedAt = now
	return nil
}
