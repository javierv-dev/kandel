package service

import (
	"context"
	"errors"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

// addJournal inserta una entrada de bitácora con un sync_id estable en el fake y devuelve su id local.
func addJournal(t *testing.T, m *fakeObs, syncID, title string) int64 {
	t.Helper()
	id, err := m.Add(context.Background(), domain.Memory{
		SyncID: syncID, Project: "kandel", Type: domain.TypeJournal, Title: title, Content: title,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return id
}

func newThreadSvc(m *fakeObs, r *fakeRelations) *ThreadService {
	return NewThreadService(m, r, passthroughUoW{}, fixedClock{})
}

func TestThreadReconstructsChainOldestToNewest(t *testing.T) {
	ctx := context.Background()
	mem := newFakeObs()
	rels := &fakeRelations{}
	a := addJournal(t, mem, "mem-a", "Fase 1")
	b := addJournal(t, mem, "mem-b", "Fase 2")
	c := addJournal(t, mem, "mem-c", "Fase 3")
	svc := newThreadSvc(mem, rels)

	if _, err := svc.Link(ctx, b, a); err != nil {
		t.Fatalf("Link B→A: %v", err)
	}
	if _, err := svc.Link(ctx, c, b); err != nil {
		t.Fatalf("Link C→B: %v", err)
	}

	chain, err := svc.Thread(ctx, c)
	if err != nil {
		t.Fatalf("Thread: %v", err)
	}
	if len(chain) != 3 {
		t.Fatalf("se esperaban 3 eslabones, hubo %d: %+v", len(chain), chain)
	}
	// Orden cronológico: antigua → nueva.
	if chain[0].Title != "Fase 1" || chain[1].Title != "Fase 2" || chain[2].Title != "Fase 3" {
		t.Errorf("orden incorrecto: %q, %q, %q", chain[0].Title, chain[1].Title, chain[2].Title)
	}
}

func TestThreadSingleWhenNoLinks(t *testing.T) {
	ctx := context.Background()
	mem := newFakeObs()
	id := addJournal(t, mem, "mem-solo", "Fase única")
	chain, err := newThreadSvc(mem, &fakeRelations{}).Thread(ctx, id)
	if err != nil {
		t.Fatalf("Thread: %v", err)
	}
	if len(chain) != 1 || chain[0].Title != "Fase única" {
		t.Errorf("sin enlaces debe devolver solo la propia entrada, dio: %+v", chain)
	}
}

func TestLinkIsIdempotent(t *testing.T) {
	ctx := context.Background()
	mem := newFakeObs()
	rels := &fakeRelations{}
	a := addJournal(t, mem, "mem-a", "Fase 1")
	b := addJournal(t, mem, "mem-b", "Fase 2")
	svc := newThreadSvc(mem, rels)

	r1, err := svc.Link(ctx, b, a)
	if err != nil {
		t.Fatalf("Link 1: %v", err)
	}
	r2, err := svc.Link(ctx, b, a)
	if err != nil {
		t.Fatalf("Link 2: %v", err)
	}
	if r1.ID != r2.ID {
		t.Errorf("Link repetido debe devolver la misma relación, no duplicar: %d vs %d", r1.ID, r2.ID)
	}
	if len(rels.items) != 1 {
		t.Errorf("no debe haber duplicados en el repo, hubo %d", len(rels.items))
	}
}

func TestLinkSelfRejected(t *testing.T) {
	ctx := context.Background()
	mem := newFakeObs()
	a := addJournal(t, mem, "mem-a", "Fase 1")
	_, err := newThreadSvc(mem, &fakeRelations{}).Link(ctx, a, a)
	if !errors.Is(err, domain.ErrSelfContinue) {
		t.Errorf("enlazar una entrada a sí misma debe dar ErrSelfContinue, dio: %v", err)
	}
}

func TestLinkMissingMemory(t *testing.T) {
	ctx := context.Background()
	mem := newFakeObs()
	a := addJournal(t, mem, "mem-a", "Fase 1")
	_, err := newThreadSvc(mem, &fakeRelations{}).Link(ctx, a, 999)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("enlazar a una memoria inexistente debe dar ErrNotFound, dio: %v", err)
	}
}

func TestThreadNotFound(t *testing.T) {
	_, err := newThreadSvc(newFakeObs(), &fakeRelations{}).Thread(context.Background(), 42)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Thread de id inexistente debe dar ErrNotFound, dio: %v", err)
	}
}

func TestThreadBreaksOnCycle(t *testing.T) {
	ctx := context.Background()
	mem := newFakeObs()
	rels := &fakeRelations{}
	a := addJournal(t, mem, "mem-a", "A")
	b := addJournal(t, mem, "mem-b", "B")
	svc := newThreadSvc(mem, rels)
	// Ciclo artificial A→B y B→A: no debe colgarse ni exceder el conjunto de vistos.
	if _, err := svc.Link(ctx, a, b); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Link(ctx, b, a); err != nil {
		t.Fatal(err)
	}
	chain, err := svc.Thread(ctx, a)
	if err != nil {
		t.Fatalf("Thread: %v", err)
	}
	if len(chain) != 2 {
		t.Errorf("un ciclo A↔B debe visitarse una vez cada uno (2), dio %d", len(chain))
	}
}
