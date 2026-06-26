package sqlite

import (
	"context"
	"testing"
	"time"

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

	if err := repo.End(ctx, "s1", "resumen final"); err != nil {
		t.Fatalf("End: %v", err)
	}
	got, _ = repo.Get(ctx, "s1")
	if got.IsOpen() {
		t.Error("tras End la sesión debe estar cerrada")
	}
	if got.Summary != "resumen final" {
		t.Errorf("summary no coincide: %q", got.Summary)
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

func TestSessionMostRecentActive(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	if _, ok, _ := repo.MostRecentActive(ctx, "proj"); ok {
		t.Fatal("sin sesiones no debe haber activa")
	}

	older := domain.Session{ID: "old", Project: "proj", Directory: "/d", StartedAt: baseTime}
	newer := domain.Session{ID: "new", Project: "proj", Directory: "/d", StartedAt: baseTime.Add(time.Hour)}
	if err := repo.Create(ctx, older); err != nil {
		t.Fatalf("Create older: %v", err)
	}
	if err := repo.Create(ctx, newer); err != nil {
		t.Fatalf("Create newer: %v", err)
	}

	id, ok, err := repo.MostRecentActive(ctx, "proj")
	if err != nil {
		t.Fatalf("MostRecentActive: %v", err)
	}
	if !ok || id != "new" {
		t.Errorf("debió devolver la más reciente 'new', fue %q (ok=%v)", id, ok)
	}

	// Tras cerrar la más reciente, la activa pasa a ser la otra.
	if err := repo.End(ctx, "new", ""); err != nil {
		t.Fatalf("End: %v", err)
	}
	id, ok, _ = repo.MostRecentActive(ctx, "proj")
	if !ok || id != "old" {
		t.Errorf("tras cerrar 'new' la activa debe ser 'old', fue %q (ok=%v)", id, ok)
	}
}

func TestSessionRecentFiltersByProject(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, domain.Session{ID: "a", Project: "proj", Directory: "/d", StartedAt: baseTime})
	_ = repo.Create(ctx, domain.Session{ID: "b", Project: "otro", Directory: "/d", StartedAt: baseTime})

	got, err := repo.Recent(ctx, "proj", 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("Recent debió devolver solo la sesión de 'proj', fue %+v", got)
	}
}
