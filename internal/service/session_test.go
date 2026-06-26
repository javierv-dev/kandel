package service

import (
	"context"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

func newSessionFixture() (*SessionService, *fakeSessions, *fakeSync) {
	sessions := newFakeSessions()
	sync := &fakeSync{}
	svc := NewSessionService(sessions, sync, passthroughUoW{}, fixedClock{now: clockNow})
	return svc, sessions, sync
}

func TestSessionStart(t *testing.T) {
	svc, sessions, sync := newSessionFixture()
	s, err := svc.Start(context.Background(), StartInput{ID: "s1", Project: "Proj", Directory: "/d"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.Project != "proj" {
		t.Errorf("el proyecto debe normalizarse, fue %q", s.Project)
	}
	if !s.StartedAt.Equal(clockNow) {
		t.Errorf("started_at debe venir del reloj, fue %v", s.StartedAt)
	}
	if len(sessions.items) != 1 {
		t.Errorf("debe crearse 1 sesión, hay %d", len(sessions.items))
	}
	if len(sync.enqueued) != 1 || sync.enqueued[0].Entity != domain.SyncEntitySession {
		t.Errorf("debe encolarse 1 mutación de sesión, fue %+v", sync.enqueued)
	}
}

func TestSessionEnd(t *testing.T) {
	svc, sessions, sync := newSessionFixture()
	ctx := context.Background()
	if _, err := svc.Start(ctx, StartInput{ID: "s1", Project: "proj", Directory: "/d"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := svc.End(ctx, "s1", "resumen"); err != nil {
		t.Fatalf("End: %v", err)
	}
	if sessions.items["s1"].IsOpen() {
		t.Error("tras End la sesión debe estar cerrada")
	}
	if sessions.items["s1"].Summary != "resumen" {
		t.Errorf("summary no coincide: %q", sessions.items["s1"].Summary)
	}
	// Start + End encolan una mutación cada uno.
	if len(sync.enqueued) != 2 {
		t.Errorf("esperaba 2 mutaciones, hubo %d", len(sync.enqueued))
	}
}

func TestSessionEndMissing(t *testing.T) {
	svc, _, _ := newSessionFixture()
	err := svc.End(context.Background(), "nope", "x")
	if err != domain.ErrNotFound {
		t.Errorf("End de sesión inexistente debe dar ErrNotFound, fue %v", err)
	}
}

func TestSessionMostRecentActive(t *testing.T) {
	svc, _, _ := newSessionFixture()
	ctx := context.Background()
	_, _ = svc.Start(ctx, StartInput{ID: "s1", Project: "proj", Directory: "/d"})

	id, ok, err := svc.MostRecentActive(ctx, "Proj")
	if err != nil {
		t.Fatalf("MostRecentActive: %v", err)
	}
	if !ok || id != "s1" {
		t.Errorf("debe resolver la sesión activa 's1', fue %q (ok=%v)", id, ok)
	}
}
