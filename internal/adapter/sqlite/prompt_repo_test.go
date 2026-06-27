package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
)

func samplePrompt() domain.Prompt {
	return domain.Prompt{
		SyncID:    "prompt-1",
		SessionID: "s1",
		Project:   "proj",
		Content:   "¿cómo implemento el repo?",
		CreatedAt: baseTime,
	}
}

func TestPromptAddAndRecent(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewPromptRepo(db)
	ctx := context.Background()

	id, err := repo.Add(ctx, samplePrompt())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id == 0 {
		t.Fatal("Add devolvió id 0")
	}

	got, err := repo.Recent(ctx, "proj", 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("esperaba 1 prompt, obtuve %d", len(got))
	}
	if got[0].Content != samplePrompt().Content || !got[0].CreatedAt.Equal(baseTime) {
		t.Errorf("contenido/created no coinciden: %+v", got[0])
	}
}

func TestPromptAddIfMissing(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewPromptRepo(db)
	ctx := context.Background()

	id1, inserted, err := repo.AddIfMissing(ctx, samplePrompt())
	if err != nil {
		t.Fatalf("AddIfMissing 1: %v", err)
	}
	if !inserted {
		t.Error("el primer AddIfMissing debe insertar")
	}

	// Mismo contenido/sesión/proyecto: no debe insertar de nuevo.
	id2, inserted, err := repo.AddIfMissing(ctx, samplePrompt())
	if err != nil {
		t.Fatalf("AddIfMissing 2: %v", err)
	}
	if inserted {
		t.Error("el segundo AddIfMissing idéntico no debe insertar")
	}
	if id1 != id2 {
		t.Errorf("debe devolver el id existente: %d vs %d", id1, id2)
	}

	got, _ := repo.Recent(ctx, "proj", 10)
	if len(got) != 1 {
		t.Errorf("debe haber un único prompt, hay %d", len(got))
	}
}

func TestPromptDelete(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewPromptRepo(db)
	ctx := context.Background()

	id, _ := repo.Add(ctx, samplePrompt())
	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := repo.Recent(ctx, "proj", 10)
	if len(got) != 0 {
		t.Errorf("tras Delete no debe quedar ningún prompt, hay %d", len(got))
	}
}

func TestPromptUpdateProject(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewPromptRepo(db)
	ctx := context.Background()

	_, _ = repo.Add(ctx, samplePrompt())

	n, err := repo.UpdateProject(ctx, "proj", "proj-nuevo")
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if n != 1 {
		t.Fatalf("esperaba 1 fila migrada, fueron %d", n)
	}
	if got, _ := repo.Recent(ctx, "proj", 10); len(got) != 0 {
		t.Errorf("no debe quedar nada bajo el nombre viejo, hay %d", len(got))
	}
	if got, _ := repo.Recent(ctx, "proj-nuevo", 10); len(got) != 1 {
		t.Errorf("debe estar bajo el nombre nuevo, hay %d", len(got))
	}
}

func TestPromptRecentOrdering(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewPromptRepo(db)
	ctx := context.Background()

	first := samplePrompt()
	first.Content = "primero"
	first.CreatedAt = baseTime
	_, _ = repo.Add(ctx, first)
	second := samplePrompt()
	second.SyncID = "prompt-2"
	second.Content = "segundo"
	second.CreatedAt = baseTime.Add(time.Hour)
	_, _ = repo.Add(ctx, second)

	got, _ := repo.Recent(ctx, "proj", 10)
	if len(got) != 2 || got[0].Content != "segundo" {
		t.Errorf("el más reciente debe ir primero, fue %+v", got)
	}
}
