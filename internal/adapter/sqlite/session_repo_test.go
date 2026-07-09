package sqlite

import (
	"context"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

func TestSessionCreateGetAndEnd(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	in := domain.Session{
		ID:        "s1",
		Project:   "proj",
		Directory: "/dir",
		StartedAt: baseTime,
	}
	if err := repo.Create(ctx, in); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get devolvió nil para una sesión existente")
	}
	if !got.IsOpen() {
		t.Error("una sesión recién creada debe estar abierta")
	}
	if !got.StartedAt.Equal(baseTime) {
		t.Errorf("started_at no coincide: %v", got.StartedAt)
	}

	if err := repo.End(ctx, "s1"); err != nil {
		t.Fatalf("End: %v", err)
	}
	got, _ = repo.Get(ctx, "s1")
	if got.IsOpen() {
		t.Error("tras End la sesión debe estar cerrada")
	}
}

func TestSessionGetMissing(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	got, err := repo.Get(context.Background(), "nope")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("Get de sesión inexistente debe ser nil, fue %+v", got)
	}
}
