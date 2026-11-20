package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/javierv-dev/kandel/internal/adapter/clock"
	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/service"
)

func TestHandoffInterruptedBetweenCheckpoints(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(t.TempDir(), "handoff.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	bind := func(db *DB) (*SessionRepo, *service.HandoffService) {
		sessions := NewSessionRepo(db)
		return sessions, service.NewHandoffService(NewHandoffRepo(db), sessions, NewUnitOfWork(db), clock.System{})
	}
	sessions, h := bind(db)
	for _, app := range []string{"claude-code", "antigravity", "pi"} {
		if err := sessions.Create(ctx, domain.Session{ID: app + ":same", ConversationID: "same", Application: app, Directory: dir, Project: "api", StartedAt: baseTime}); err != nil {
			t.Fatal(err)
		}
	}
	if empty, err := h.ListSession(ctx, "antigravity:same"); err != nil || len(empty) != 0 {
		t.Fatalf("before first checkpoint: %+v, %v", empty, err)
	}
	in := service.HandoffInput{SessionID: "claude-code:same", RequestID: "first", Title: "Orders endpoint", Content: "Test de autenticación pendiente"}
	first, err := h.Save(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := h.Save(ctx, in); err != nil || again.ID != first.ID {
		t.Fatalf("retry: %+v, %v", again, err)
	}
	// Simulate a change followed by interruption without a closing hook or another save.
	if err := os.WriteFile(filepath.Join(dir, "orders.go"), []byte("package orders"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	sessions, h = bind(db)
	if s, err := sessions.Get(ctx, "claude-code:same"); err != nil || s == nil || !s.IsOpen() || s.ConversationID != "same" {
		t.Fatalf("persisted session: %+v, %v", s, err)
	}
	if list, err := h.ListSession(ctx, "antigravity:same"); err != nil || len(list) != 1 || list[0].ID != first.ID || list[0].Content != "" {
		t.Fatalf("index: %+v, %v", list, err)
	}
	for range 2 {
		selected, err := h.Resume(ctx, "antigravity:same", first.ID)
		if err != nil || selected.Application != "claude-code" || selected.Content != in.Content {
			t.Fatalf("resume: %+v, %v", selected, err)
		}
	}
	next, err := h.Save(ctx, service.HandoffInput{SessionID: "antigravity:same", RequestID: "second", ParentID: first.ID, Title: in.Title, Content: "Repetí el test y corregí el montaje"})
	if err != nil || next.TaskID != first.TaskID || next.Application != "antigravity" {
		t.Fatalf("advance: %+v, %v", next, err)
	}
	if resumedInPi, err := h.Resume(ctx, "pi:same", next.ID); err != nil || resumedInPi.Application != "antigravity" {
		t.Fatalf("resume in pi: %+v, %v", resumedInPi, err)
	}
	third, err := h.Save(ctx, service.HandoffInput{SessionID: "pi:same", RequestID: "third", ParentID: next.ID, Title: in.Title, Content: "Continuado en Pi Agent con pruebas en verde"})
	if err != nil || third.TaskID != first.TaskID || third.Application != "pi" {
		t.Fatalf("advance in pi: %+v, %v", third, err)
	}
	if _, err := h.Save(ctx, service.HandoffInput{SessionID: in.SessionID, RequestID: "stale", ParentID: first.ID, Title: in.Title, Content: "Concurrent change"}); err == nil {
		t.Fatal("stale parent accepted")
	}
	if got, err := h.GetSession(ctx, "antigravity:same", first.ID); err != nil || got.Content != in.Content {
		t.Fatalf("original revision: %+v, %v", got, err)
	}
	if _, err := h.Get(ctx, t.TempDir(), first.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-worktree read: %v", err)
	}
	if _, err := h.ListSession(ctx, ""); err == nil {
		t.Fatal("session was inferred")
	}
}
