package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

// fakeThread es un ThreadPort en memoria para probar los handlers.
type fakeThread struct {
	linked  [2]int64
	linkErr error
	chain   []domain.Memory
}

func (f *fakeThread) Link(_ context.Context, newerID, olderID int64) (domain.MemoryRelation, error) {
	f.linked = [2]int64{newerID, olderID}
	if f.linkErr != nil {
		return domain.MemoryRelation{}, f.linkErr
	}
	return domain.MemoryRelation{Kind: domain.RelationContinues}, nil
}

func (f *fakeThread) Thread(_ context.Context, _ int64) ([]domain.Memory, error) {
	return f.chain, nil
}

var _ ThreadPort = (*fakeThread)(nil)

func TestHandleLinkPassesIDs(t *testing.T) {
	ft := &fakeThread{}
	res, err := handleLink(Services{Thread: ft})(context.Background(), callWith(map[string]any{
		"from_id": float64(3), "to_id": float64(2),
	}))
	if err != nil || res.IsError {
		t.Fatalf("handleLink falló: %v / %v", err, textOf(t, res))
	}
	if ft.linked != [2]int64{3, 2} {
		t.Errorf("ids mal traducidos: %+v", ft.linked)
	}
}

func TestHandleLinkRequiresBothIDs(t *testing.T) {
	res, _ := handleLink(Services{Thread: &fakeThread{}})(context.Background(), callWith(map[string]any{"from_id": float64(3)}))
	if !res.IsError {
		t.Error("mem_link sin to_id debe devolver error de tool")
	}
}

func TestHandleLinkSelfContinue(t *testing.T) {
	ft := &fakeThread{linkErr: domain.ErrSelfContinue}
	res, _ := handleLink(Services{Thread: ft})(context.Background(), callWith(map[string]any{
		"from_id": float64(5), "to_id": float64(9),
	}))
	if !res.IsError {
		t.Error("ErrSelfContinue debe traducirse a error de tool")
	}
}

func TestHandleThreadReturnsChain(t *testing.T) {
	ft := &fakeThread{chain: []domain.Memory{
		{ID: 1, Type: domain.TypeJournal, Title: "Fase 1", Content: "cuerpo 1"},
		{ID: 2, Type: domain.TypeJournal, Title: "Fase 2", Content: "cuerpo 2"},
	}}
	res, err := handleThread(Services{Thread: ft})(context.Background(), callWith(map[string]any{"id": float64(2)}))
	if err != nil || res.IsError {
		t.Fatalf("handleThread falló: %v / %v", err, textOf(t, res))
	}
	out := textOf(t, res)
	// El hilo trae el cuerpo COMPLETO de cada entrada, en orden.
	for _, want := range []string{"Fase 1", "cuerpo 1", "Fase 2", "cuerpo 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("la salida no contiene %q: %s", want, out)
		}
	}
}

func TestHandleThreadRequiresID(t *testing.T) {
	res, _ := handleThread(Services{Thread: &fakeThread{}})(context.Background(), callWith(nil))
	if !res.IsError {
		t.Error("mem_thread sin id debe devolver error de tool")
	}
}
