package service

import (
	"context"
	"errors"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// pendingIndex es un VectorIndex doble centrado en el backfill: sirve listas de pendientes
// preprogramadas (una por llamada a PendingEmbedding) y registra los Upsert recibidos. Cuando se
// agotan los lotes programados, devuelve vacío (no hay más pendientes).
type pendingIndex struct {
	batches  [][]int64
	call     int
	upserted map[int64]string // memoryID → modelo guardado
	failNext bool
}

func newPendingIndex(batches ...[]int64) *pendingIndex {
	return &pendingIndex{batches: batches, upserted: map[int64]string{}}
}

func (p *pendingIndex) Upsert(_ context.Context, memoryID int64, _ []float32, model string) error {
	p.upserted[memoryID] = model
	return nil
}
func (p *pendingIndex) Nearest(context.Context, []float32, int) ([]ports.Neighbor, error) {
	return nil, nil
}

func (p *pendingIndex) Delete(context.Context, int64) error { return nil }

func (p *pendingIndex) PendingEmbedding(_ context.Context, _ string, _ int) ([]int64, error) {
	if p.failNext {
		return nil, errors.New("fallo del índice")
	}
	if p.call >= len(p.batches) {
		return nil, nil
	}
	b := p.batches[p.call]
	p.call++
	return b, nil
}

// newBackfillService arma un EmbeddingService con repo y embebedor falsos del paquete y el
// índice de pendientes dado.
func newBackfillService(idx *pendingIndex, repo *fakeObs, emb *fakeEmbedder) *EmbeddingService {
	return &EmbeddingService{memories: repo, embedder: emb, vectors: idx, batch: 100}
}

func seedMemory(t *testing.T, repo *fakeObs, content string) int64 {
	t.Helper()
	id, err := repo.Add(context.Background(), domain.Memory{
		Type: domain.TypeDecision, Scope: domain.ScopeProject,
		Title: "t", Content: content,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return id
}

func TestBackfillEmbedsAllPending(t *testing.T) {
	repo := newFakeObs()
	a := seedMemory(t, repo, "uno")
	b := seedMemory(t, repo, "dos")
	emb := newFakeEmbedder()
	// Dos lotes: el segundo vacío implícito al agotarse las listas programadas.
	idx := newPendingIndex([]int64{a, b})
	svc := newBackfillService(idx, repo, emb)

	res, err := svc.Backfill(context.Background())
	if err != nil {
		t.Fatalf("backfill devolvió error: %v", err)
	}
	if res.Embedded != 2 {
		t.Errorf("Embedded = %d, se esperaba 2", res.Embedded)
	}
	if idx.upserted[a] != "fake" || idx.upserted[b] != "fake" {
		t.Errorf("no se guardaron ambos vectores con el modelo: %+v", idx.upserted)
	}
}

func TestBackfillSkipsMissingMemory(t *testing.T) {
	repo := newFakeObs()
	a := seedMemory(t, repo, "uno")
	emb := newFakeEmbedder()
	// El id 999 no existe: debe saltarse sin abortar; el lote progresa por 'a'.
	idx := newPendingIndex([]int64{a, 999})
	svc := newBackfillService(idx, repo, emb)

	res, err := svc.Backfill(context.Background())
	if err != nil {
		t.Fatalf("backfill devolvió error: %v", err)
	}
	if res.Embedded != 1 {
		t.Errorf("Embedded = %d, se esperaba 1", res.Embedded)
	}
}

func TestBackfillAbortsOnEmbedError(t *testing.T) {
	repo := newFakeObs()
	seedMemory(t, repo, "uno")
	emb := newFakeEmbedder()
	emb.failWith = errors.New("ollama caído")
	idx := newPendingIndex([]int64{1})
	svc := newBackfillService(idx, repo, emb)

	_, err := svc.Backfill(context.Background())
	if err == nil {
		t.Fatal("se esperaba error por fallo del embebedor")
	}
}

func TestReembedSingle(t *testing.T) {
	repo := newFakeObs()
	id := seedMemory(t, repo, "contenido")
	emb := newFakeEmbedder()
	idx := newPendingIndex()
	svc := newBackfillService(idx, repo, emb)

	if err := svc.Reembed(context.Background(), id); err != nil {
		t.Fatalf("reembed devolvió error: %v", err)
	}
	if idx.upserted[id] != "fake" {
		t.Errorf("no se guardó el vector recalculado: %+v", idx.upserted)
	}
}

func TestReembedNotFound(t *testing.T) {
	repo := newFakeObs()
	emb := newFakeEmbedder()
	idx := newPendingIndex()
	svc := newBackfillService(idx, repo, emb)

	err := svc.Reembed(context.Background(), 404)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("se esperaba ErrNotFound, se obtuvo %v", err)
	}
}

// TestSweepBackfillEmbedsPending verifica que la variante automática hace el mismo trabajo que
// Backfill cuando no hay otro en curso (skipped=false).
func TestSweepBackfillEmbedsPending(t *testing.T) {
	repo := newFakeObs()
	a := seedMemory(t, repo, "uno")
	emb := newFakeEmbedder()
	idx := newPendingIndex([]int64{a})
	svc := newBackfillService(idx, repo, emb)

	res, skipped, err := svc.SweepBackfill(context.Background())
	if err != nil {
		t.Fatalf("SweepBackfill devolvió error: %v", err)
	}
	if skipped {
		t.Error("no debe omitirse si no hay otro backfill en curso")
	}
	if res.Embedded != 1 {
		t.Errorf("Embedded = %d, se esperaba 1", res.Embedded)
	}
}

// TestSweepBackfillSkipsWhenBusy verifica la coalescencia: si ya hay un backfill corriendo (lock
// tomado), SweepBackfill se omite en vez de encolar otra pasada.
func TestSweepBackfillSkipsWhenBusy(t *testing.T) {
	repo := newFakeObs()
	emb := newFakeEmbedder()
	idx := newPendingIndex()
	svc := newBackfillService(idx, repo, emb)

	svc.mu.Lock() // simula un backfill en curso
	defer svc.mu.Unlock()

	_, skipped, err := svc.SweepBackfill(context.Background())
	if err != nil {
		t.Fatalf("SweepBackfill devolvió error: %v", err)
	}
	if !skipped {
		t.Error("debe omitirse cuando ya hay un backfill en curso")
	}
}

// TestBackfillRunsPostHook verifica que tras un backfill exitoso corre el enganche posterior (la
// detección de conflictos diferida, §20.F).
func TestBackfillRunsPostHook(t *testing.T) {
	repo := newFakeObs()
	a := seedMemory(t, repo, "uno")
	emb := newFakeEmbedder()
	idx := newPendingIndex([]int64{a})
	svc := newBackfillService(idx, repo, emb)
	called := 0
	svc.postBackfill = func(context.Context) { called++ }

	if _, err := svc.Backfill(context.Background()); err != nil {
		t.Fatalf("backfill devolvió error: %v", err)
	}
	if called != 1 {
		t.Errorf("el enganche posterior debe correr una vez, corrió %d", called)
	}
}

// TestBackfillSkipsPostHookOnError verifica que si el backfill falla (embebedor caído) NO corre el
// enganche: sin embebedor, la detección tampoco podría.
func TestBackfillSkipsPostHookOnError(t *testing.T) {
	repo := newFakeObs()
	seedMemory(t, repo, "uno")
	emb := newFakeEmbedder()
	emb.failWith = errors.New("ollama caído")
	idx := newPendingIndex([]int64{1})
	svc := newBackfillService(idx, repo, emb)
	called := 0
	svc.postBackfill = func(context.Context) { called++ }

	if _, err := svc.Backfill(context.Background()); err == nil {
		t.Fatal("se esperaba error del embebedor")
	}
	if called != 0 {
		t.Errorf("el enganche no debe correr si el backfill falló, corrió %d", called)
	}
}

// TestWarmupCallsEmbedder verifica que el warm-up dispara una petición al embebedor y propaga su
// error (para que el handler lo registre).
func TestWarmupCallsEmbedder(t *testing.T) {
	repo := newFakeObs()
	idx := newPendingIndex()

	emb := newFakeEmbedder()
	svc := newBackfillService(idx, repo, emb)
	if err := svc.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup no debe fallar con el embebedor sano: %v", err)
	}

	embDown := newFakeEmbedder()
	embDown.failWith = errors.New("ollama caído")
	svcDown := newBackfillService(idx, repo, embDown)
	if err := svcDown.Warmup(context.Background()); err == nil {
		t.Error("Warmup debe propagar el error del embebedor caído")
	}
}
