package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

func newComponentRepo(t *testing.T) (ComponentRepository, *sql.DB) {
	t.Helper()

	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	return NewComponentRepository(db), db
}

func newTestComponent(nome string) *Component {
	return &Component{
		Nome:          nome,
		Descricao:     "componente de teste",
		Source:        json.RawMessage(`{"repoUrl":"https://example.com/repo.git","ref":{"type":"branch","value":"main"}}`),
		Resources:     json.RawMessage(`{"requests":{"cpu":"100m"}}`),
		Runtime:       json.RawMessage(`{"workloadKind":"Pod"}`),
		TargetContext: json.RawMessage(`{"cluster":"minikube"}`),
	}
}

func TestComponentRepository_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	repo, _ := newComponentRepo(t)

	c := newTestComponent("meu-componente")
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if c.ID == "" {
		t.Fatal("Create() did not populate ID")
	}
	if c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
		t.Fatal("Create() did not populate CreatedAt/UpdatedAt")
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Nome != c.Nome {
		t.Errorf("Nome = %q, want %q", got.Nome, c.Nome)
	}
	if got.Descricao != c.Descricao {
		t.Errorf("Descricao = %q, want %q", got.Descricao, c.Descricao)
	}
	if string(got.Source) != string(c.Source) {
		t.Errorf("Source = %s, want %s", got.Source, c.Source)
	}
	if got.Build != nil {
		t.Errorf("Build = %s, want nil (never set)", got.Build)
	}
	if !got.CreatedAt.Equal(c.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, c.CreatedAt)
	}
}

func TestComponentRepository_GetNotFound(t *testing.T) {
	ctx := context.Background()
	repo, _ := newComponentRepo(t)

	_, err := repo.Get(ctx, "id-inexistente")
	if !errors.Is(err, ErrComponentNotFound) {
		t.Fatalf("Get() error = %v, want ErrComponentNotFound", err)
	}
}

func TestComponentRepository_List(t *testing.T) {
	ctx := context.Background()
	repo, _ := newComponentRepo(t)

	empty, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("List() on empty repo = %d items, want 0", len(empty))
	}

	a := newTestComponent("componente-a")
	b := newTestComponent("componente-b")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create(a) error = %v", err)
	}
	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("Create(b) error = %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() = %d items, want 2", len(list))
	}
	if list[0].ID != a.ID || list[1].ID != b.ID {
		t.Errorf("List() order = [%s, %s], want [%s, %s]", list[0].ID, list[1].ID, a.ID, b.ID)
	}
}

func TestComponentRepository_Update(t *testing.T) {
	ctx := context.Background()
	repo, _ := newComponentRepo(t)

	c := newTestComponent("nome-original")
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	createdAt := c.CreatedAt

	c.Nome = "nome-atualizado"
	c.Descricao = "descricao-atualizada"
	c.Runtime = json.RawMessage(`{"workloadKind":"Job"}`)
	if err := repo.Update(ctx, c); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Nome != "nome-atualizado" {
		t.Errorf("Nome = %q, want %q", got.Nome, "nome-atualizado")
	}
	if got.Descricao != "descricao-atualizada" {
		t.Errorf("Descricao = %q, want %q", got.Descricao, "descricao-atualizada")
	}
	if string(got.Runtime) != `{"workloadKind":"Job"}` {
		t.Errorf("Runtime = %s, want %s", got.Runtime, `{"workloadKind":"Job"}`)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt changed on Update: got %v, want %v", got.CreatedAt, createdAt)
	}
	if !got.UpdatedAt.After(createdAt) && !got.UpdatedAt.Equal(got.CreatedAt) {
		t.Errorf("UpdatedAt = %v, want >= CreatedAt %v", got.UpdatedAt, createdAt)
	}
}

func TestComponentRepository_UpdateNotFound(t *testing.T) {
	ctx := context.Background()
	repo, _ := newComponentRepo(t)

	c := newTestComponent("nao-existe")
	c.ID = "id-inexistente"
	err := repo.Update(ctx, c)
	if !errors.Is(err, ErrComponentNotFound) {
		t.Fatalf("Update() error = %v, want ErrComponentNotFound", err)
	}
}

func TestComponentRepository_Delete(t *testing.T) {
	ctx := context.Background()
	repo, _ := newComponentRepo(t)

	c := newTestComponent("para-remover")
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := repo.Delete(ctx, c.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err := repo.Get(ctx, c.ID)
	if !errors.Is(err, ErrComponentNotFound) {
		t.Fatalf("Get() after Delete() error = %v, want ErrComponentNotFound", err)
	}
}

func TestComponentRepository_DeleteNotFound(t *testing.T) {
	ctx := context.Background()
	repo, _ := newComponentRepo(t)

	err := repo.Delete(ctx, "id-inexistente")
	if !errors.Is(err, ErrComponentNotFound) {
		t.Fatalf("Delete() error = %v, want ErrComponentNotFound", err)
	}
}

func TestComponentRepository_DeleteBlockedByExecutions(t *testing.T) {
	ctx := context.Background()
	repo, db := newComponentRepo(t)

	c := newTestComponent("com-execucoes")
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO executions (id, component_id, phase) VALUES (?, ?, 'Pending')`, "exec-1", c.ID); err != nil {
		t.Fatalf("inserting execution: %v", err)
	}

	err := repo.Delete(ctx, c.ID)
	if err == nil {
		t.Fatal("Delete() with existing executions: expected error, got nil")
	}
	if errors.Is(err, ErrComponentNotFound) {
		t.Fatalf("Delete() error = %v, want a foreign key violation, not ErrComponentNotFound", err)
	}

	if _, err := repo.Get(ctx, c.ID); err != nil {
		t.Fatalf("Get() after blocked Delete() error = %v, want component to still exist", err)
	}
}
