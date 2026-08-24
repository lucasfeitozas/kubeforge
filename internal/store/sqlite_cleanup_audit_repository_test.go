package store

import (
	"context"
	"database/sql"
	"testing"
)

func newCleanupAuditRepo(t *testing.T) (CleanupAuditRepository, *sql.DB) {
	t.Helper()

	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	return NewCleanupAuditRepository(db), db
}

func TestCleanupAuditRepository_Record(t *testing.T) {
	ctx := context.Background()
	repo, db := newCleanupAuditRepo(t)

	entry := &CleanupAuditEntry{
		ResourceKind: "Job",
		ResourceName: "componente-teste",
		Namespace:    "kubeforge-workloads",
	}
	if err := repo.Record(ctx, entry); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if entry.ID == "" {
		t.Fatal("Record() não preencheu ID")
	}
	if entry.RemovedAt.IsZero() {
		t.Fatal("Record() não preencheu RemovedAt")
	}

	var kind, name, namespace string
	row := db.QueryRowContext(ctx, `SELECT resource_kind, resource_name, namespace FROM cleanup_audit_log WHERE id = ?`, entry.ID)
	if err := row.Scan(&kind, &name, &namespace); err != nil {
		t.Fatalf("consultando cleanup_audit_log: %v", err)
	}
	if kind != "Job" || name != "componente-teste" || namespace != "kubeforge-workloads" {
		t.Fatalf("linha persistida = (%q, %q, %q), esperava (Job, componente-teste, kubeforge-workloads)", kind, name, namespace)
	}
}

func TestCleanupAuditRepository_RecordMultiplasEntradas(t *testing.T) {
	ctx := context.Background()
	repo, db := newCleanupAuditRepo(t)

	for _, kind := range []string{"Job", "Pod", "PersistentVolumeClaim"} {
		entry := &CleanupAuditEntry{ResourceKind: kind, ResourceName: "recurso-" + kind, Namespace: "default"}
		if err := repo.Record(ctx, entry); err != nil {
			t.Fatalf("Record(%q) error = %v", kind, err)
		}
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cleanup_audit_log`).Scan(&count); err != nil {
		t.Fatalf("contando cleanup_audit_log: %v", err)
	}
	if count != 3 {
		t.Fatalf("cleanup_audit_log tem %d linhas, esperava 3", count)
	}
}
