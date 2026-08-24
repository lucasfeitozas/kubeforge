package store

import (
	"context"
	"time"
)

// CleanupAuditEntry registra um recurso removido por um cleanup
// (internal/controller.RunCleanup), satisfazendo o critério de aceite
// "log de auditoria simples (o que foi removido e quando)" da E5.S2.
type CleanupAuditEntry struct {
	ID           string
	ResourceKind string
	ResourceName string
	Namespace    string
	RemovedAt    time.Time
}

// CleanupAuditRepository persiste o log de auditoria de remoções feitas
// por internal/controller.RunCleanup.
type CleanupAuditRepository interface {
	// Record insere uma entrada de auditoria, preenchendo ID e RemovedAt.
	Record(ctx context.Context, entry *CleanupAuditEntry) error
}
