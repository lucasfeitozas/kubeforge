package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func newMigratedDB(t *testing.T) *sql.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "kubeforge.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

func TestMigrateCreatesTables(t *testing.T) {
	db := newMigratedDB(t)

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatalf("querying sqlite_master: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scanning table name: %v", err)
		}
		tables = append(tables, name)
	}

	want := map[string]bool{"components": false, "executions": false, "schema_migrations": false}
	for _, table := range tables {
		if _, ok := want[table]; ok {
			want[table] = true
		}
	}
	for table, found := range want {
		if !found {
			t.Errorf("expected table %q to exist, tables found: %v", table, tables)
		}
	}
}

func TestMigrateColumns(t *testing.T) {
	db := newMigratedDB(t)

	cases := []struct {
		table        string
		notNullCols  []string
		nullableCols []string
	}{
		{
			table:        "components",
			notNullCols:  []string{"id", "nome", "source", "resources", "runtime", "target_context", "created_at", "updated_at"},
			nullableCols: []string{"descricao", "build", "hooks", "lifecycle"},
		},
		{
			table:        "executions",
			notNullCols:  []string{"id", "component_id", "phase", "created_at"},
			nullableCols: []string{"started_at", "completed_at", "image_digest"},
		},
	}

	for _, tc := range cases {
		rows, err := db.Query(`SELECT name, "notnull" FROM pragma_table_info(?)`, tc.table)
		if err != nil {
			t.Fatalf("PRAGMA table_info(%s): %v", tc.table, err)
		}

		notNull := map[string]bool{}
		found := map[string]bool{}
		for rows.Next() {
			var name string
			var nn int
			if err := rows.Scan(&name, &nn); err != nil {
				rows.Close()
				t.Fatalf("scanning table_info row: %v", err)
			}
			found[name] = true
			notNull[name] = nn == 1
		}
		rows.Close()

		for _, col := range tc.notNullCols {
			if !found[col] {
				t.Errorf("%s: expected column %q to exist", tc.table, col)
				continue
			}
			if !notNull[col] {
				t.Errorf("%s: expected column %q to be NOT NULL", tc.table, col)
			}
		}
		for _, col := range tc.nullableCols {
			if !found[col] {
				t.Errorf("%s: expected column %q to exist", tc.table, col)
				continue
			}
			if notNull[col] {
				t.Errorf("%s: expected column %q to be nullable", tc.table, col)
			}
		}
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	db := newMigratedDB(t)

	var fkEnabled int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fkEnabled); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fkEnabled != 1 {
		t.Fatalf("expected foreign_keys pragma to be enabled, got %d", fkEnabled)
	}

	_, err := db.Exec(`
		INSERT INTO components (id, nome, source, resources, runtime, target_context)
		VALUES ('comp-1', 'meu-componente', '{}', '{}', '{}', '{}')
	`)
	if err != nil {
		t.Fatalf("inserting component: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO executions (id, component_id, phase)
		VALUES ('exec-1', 'comp-1', 'Pending')
	`)
	if err != nil {
		t.Fatalf("inserting execution: %v", err)
	}

	if _, err := db.Exec(`DELETE FROM components WHERE id = 'comp-1'`); err == nil {
		t.Fatal("expected deleting a component with existing executions to fail (ON DELETE RESTRICT), got nil error")
	}
}

func TestExecutionPhaseCheckConstraint(t *testing.T) {
	db := newMigratedDB(t)

	_, err := db.Exec(`
		INSERT INTO components (id, nome, source, resources, runtime, target_context)
		VALUES ('comp-1', 'meu-componente', '{}', '{}', '{}', '{}')
	`)
	if err != nil {
		t.Fatalf("inserting component: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO executions (id, component_id, phase)
		VALUES ('exec-1', 'comp-1', 'NotARealPhase')
	`)
	if err == nil {
		t.Fatal("expected inserting an execution with an invalid phase to fail the CHECK constraint, got nil error")
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := newMigratedDB(t)

	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate() call error = %v", err)
	}
}
