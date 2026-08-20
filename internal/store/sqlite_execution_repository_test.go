package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func newExecutionRepo(t *testing.T) (ExecutionRepository, *sql.DB) {
	t.Helper()

	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	return NewExecutionRepository(db), db
}

// newTestComponentForExecution cria e persiste um Component, satisfazendo a
// FK executions.component_id -> components.id.
func newTestComponentForExecution(t *testing.T, db *sql.DB) *Component {
	t.Helper()
	c := newTestComponent("componente-para-execution")
	if err := NewComponentRepository(db).Create(context.Background(), c); err != nil {
		t.Fatalf("criando componente de apoio: %v", err)
	}
	return c
}

func TestExecutionRepository_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	repo, db := newExecutionRepo(t)
	c := newTestComponentForExecution(t, db)

	e := &Execution{ComponentID: c.ID}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if e.ID == "" {
		t.Fatal("Create() did not populate ID")
	}
	if e.Phase != "Pending" {
		t.Fatalf("Create() Phase = %q, want %q", e.Phase, "Pending")
	}
	if e.CreatedAt.IsZero() {
		t.Fatal("Create() did not populate CreatedAt")
	}

	got, err := repo.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != e.ID || got.ComponentID != c.ID || got.Phase != "Pending" {
		t.Fatalf("Get() = %+v, want ComponentID=%q Phase=Pending", got, c.ID)
	}
	if got.ImageTag != "" || got.BuildLog != "" {
		t.Fatalf("Get() recém-criada deveria ter ImageTag/BuildLog vazios, got ImageTag=%q BuildLog=%q", got.ImageTag, got.BuildLog)
	}
	if got.StartedAt != nil || got.CompletedAt != nil {
		t.Fatalf("Get() recém-criada deveria ter StartedAt/CompletedAt nulos, got %+v", got)
	}
}

func TestExecutionRepository_GetNaoEncontrada(t *testing.T) {
	repo, _ := newExecutionRepo(t)
	_, err := repo.Get(context.Background(), "id-inexistente")
	if !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("Get() error = %v, want ErrExecutionNotFound", err)
	}
}

func TestExecutionRepository_List(t *testing.T) {
	ctx := context.Background()
	repo, db := newExecutionRepo(t)
	c1 := newTestComponentForExecution(t, db)
	c2 := newTestComponent("outro-componente")
	if err := NewComponentRepository(db).Create(ctx, c2); err != nil {
		t.Fatalf("criando segundo componente: %v", err)
	}

	e1 := &Execution{ComponentID: c1.ID}
	e2 := &Execution{ComponentID: c1.ID}
	e3 := &Execution{ComponentID: c2.ID}
	for _, e := range []*Execution{e1, e2, e3} {
		if err := repo.Create(ctx, e); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	got, err := repo.List(ctx, c1.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List() retornou %d executions, want 2", len(got))
	}
	for _, e := range got {
		if e.ComponentID != c1.ID {
			t.Fatalf("List(%q) retornou execution de outro componente: %+v", c1.ID, e)
		}
	}
}

func TestExecutionRepository_UpdateBuildLog(t *testing.T) {
	ctx := context.Background()
	repo, db := newExecutionRepo(t)
	c := newTestComponentForExecution(t, db)

	e := &Execution{ComponentID: c.ID}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	wantTag := "kubeforge/carga-cpu:abcdef123456"
	wantLog := "Step 1/3 : FROM scratch\n...\nSuccessfully built abc123\n"
	if err := repo.UpdateBuildLog(ctx, e.ID, wantTag, wantLog); err != nil {
		t.Fatalf("UpdateBuildLog() error = %v", err)
	}

	got, err := repo.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ImageTag != wantTag {
		t.Fatalf("Get().ImageTag = %q, want %q", got.ImageTag, wantTag)
	}
	if got.BuildLog != wantLog {
		t.Fatalf("Get().BuildLog = %q, want %q", got.BuildLog, wantLog)
	}
}

func TestExecutionRepository_UpdateBuildLogNaoEncontrada(t *testing.T) {
	repo, _ := newExecutionRepo(t)
	err := repo.UpdateBuildLog(context.Background(), "id-inexistente", "tag", "log")
	if !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("UpdateBuildLog() error = %v, want ErrExecutionNotFound", err)
	}
}
