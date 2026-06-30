package service

import (
	"context"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

var clockNow = time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)

func newMemoryFixture() (*MemoryService, *fakeObs, *fakeSync) {
	obs := newFakeObs()
	sync := &fakeSync{}
	svc := NewMemoryService(obs, sync, passthroughUoW{}, fixedClock{now: clockNow}, newFakeEmbedder(), newFakeVectorIndex())
	return svc, obs, sync
}

func baseSaveInput() SaveInput {
	return SaveInput{
		SessionID: "s1",
		Project:   "Proj",
		Type:      domain.TypeDecision,
		Title:     "Usar hexagonal",
		Content:   "Se decide arquitectura hexagonal.",
	}
}

func TestSaveCreatesNew(t *testing.T) {
	svc, obs, sync := newMemoryFixture()
	res, err := svc.Save(context.Background(), baseSaveInput())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if res.Outcome != SaveCreated {
		t.Errorf("outcome esperado created, fue %v", res.Outcome)
	}
	if res.Memory.Project != "proj" {
		t.Errorf("el proyecto debe normalizarse a minúsculas, fue %q", res.Memory.Project)
	}
	if res.Memory.SyncID == "" {
		t.Error("una memoria nueva debe recibir sync_id")
	}
	// decisión decae a 6 meses.
	if !res.Memory.ReviewAfter.Equal(clockNow.AddDate(0, 6, 0)) {
		t.Errorf("review_after de decision debe ser +6 meses, fue %v", res.Memory.ReviewAfter)
	}
	if len(obs.items) != 1 {
		t.Errorf("debe haber 1 memoria, hay %d", len(obs.items))
	}
	if len(sync.enqueued) != 1 || sync.enqueued[0].Op != domain.SyncOpUpsert {
		t.Errorf("debe encolarse 1 upsert, fue %+v", sync.enqueued)
	}
}

func TestSaveDuplicateWithinWindow(t *testing.T) {
	svc, obs, sync := newMemoryFixture()
	ctx := context.Background()
	if _, err := svc.Save(ctx, baseSaveInput()); err != nil {
		t.Fatalf("Save 1: %v", err)
	}
	res, err := svc.Save(ctx, baseSaveInput())
	if err != nil {
		t.Fatalf("Save 2: %v", err)
	}
	if res.Outcome != SaveDuplicate {
		t.Errorf("la segunda igual debe ser duplicate, fue %v", res.Outcome)
	}
	if len(obs.items) != 1 {
		t.Errorf("un duplicado no debe crear fila nueva, hay %d", len(obs.items))
	}
	if res.Memory.DuplicateCount != 2 {
		t.Errorf("duplicate_count debe ser 2, fue %d", res.Memory.DuplicateCount)
	}
	if len(sync.enqueued) != 2 {
		t.Errorf("cada Save encola una mutación, hubo %d", len(sync.enqueued))
	}
}

func TestSaveDuplicateOutsideWindowCreatesNew(t *testing.T) {
	obs := newFakeObs()
	sync := &fakeSync{}
	clk := &mutableClock{now: clockNow}
	svc := NewMemoryService(obs, sync, passthroughUoW{}, clk, newFakeEmbedder(), newFakeVectorIndex())
	ctx := context.Background()

	if _, err := svc.Save(ctx, baseSaveInput()); err != nil {
		t.Fatalf("Save 1: %v", err)
	}
	// Avanzar más allá de la ventana de dedupe.
	clk.now = clockNow.Add(defaultDedupeWindow + time.Minute)
	res, err := svc.Save(ctx, baseSaveInput())
	if err != nil {
		t.Fatalf("Save 2: %v", err)
	}
	if res.Outcome != SaveCreated {
		t.Errorf("fuera de la ventana debe crear nueva, fue %v", res.Outcome)
	}
	if len(obs.items) != 2 {
		t.Errorf("debe haber 2 memorias, hay %d", len(obs.items))
	}
}

func TestSaveRequiresProjectForProjectScope(t *testing.T) {
	svc, _, _ := newMemoryFixture()
	in := baseSaveInput()
	in.Project = "   "
	_, err := svc.Save(context.Background(), in)
	if err != domain.ErrAmbiguousProject {
		t.Errorf("escribir scope project sin proyecto debe fallar con ErrAmbiguousProject, fue %v", err)
	}
}

func TestSaveGlobalScopeAllowsEmptyProject(t *testing.T) {
	svc, _, _ := newMemoryFixture()
	in := baseSaveInput()
	in.Project = ""
	in.Scope = domain.ScopeGlobal
	res, err := svc.Save(context.Background(), in)
	if err != nil {
		t.Fatalf("Save global: %v", err)
	}
	if res.Outcome != SaveCreated {
		t.Errorf("scope global sin proyecto debe crearse, fue %v", res.Outcome)
	}
}

func TestUpdateAppliesPartialChanges(t *testing.T) {
	svc, _, sync := newMemoryFixture()
	ctx := context.Background()
	res, _ := svc.Save(ctx, baseSaveInput())

	newTitle := "Título nuevo"
	newContent := "contenido nuevo"
	updated, err := svc.Update(ctx, res.Memory.ID, UpdateInput{Title: &newTitle, Content: &newContent})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Title != newTitle || updated.Content != newContent {
		t.Errorf("los cambios no se aplicaron: %+v", updated)
	}
	if updated.NormalizedHash == res.Memory.NormalizedHash {
		t.Error("al cambiar el contenido el hash debe recalcularse")
	}
	// Save + Update encolan una mutación cada uno.
	if len(sync.enqueued) != 2 {
		t.Errorf("esperaba 2 mutaciones, hubo %d", len(sync.enqueued))
	}
}

func TestUpdateMissing(t *testing.T) {
	svc, _, _ := newMemoryFixture()
	_, err := svc.Update(context.Background(), 999, UpdateInput{})
	if err != domain.ErrNotFound {
		t.Errorf("Update de id inexistente debe dar ErrNotFound, fue %v", err)
	}
}

func TestDeleteSoftAndEnqueue(t *testing.T) {
	svc, obs, sync := newMemoryFixture()
	ctx := context.Background()
	res, _ := svc.Save(ctx, baseSaveInput())

	if err := svc.Delete(ctx, res.Memory.ID, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := obs.Get(ctx, res.Memory.ID)
	if got != nil {
		t.Error("tras soft-delete la memoria no debe recuperarse")
	}
	last := sync.enqueued[len(sync.enqueued)-1]
	if last.Op != domain.SyncOpDelete {
		t.Errorf("el borrado debe encolar una mutación delete, fue %v", last.Op)
	}
}

func TestDeleteMissing(t *testing.T) {
	svc, _, _ := newMemoryFixture()
	err := svc.Delete(context.Background(), 999, false)
	if err != domain.ErrNotFound {
		t.Errorf("Delete de id inexistente debe dar ErrNotFound, fue %v", err)
	}
}

func TestRestoreReindexesAndEnqueuesUpsert(t *testing.T) {
	svc, obs, sync := newMemoryFixture()
	ctx := context.Background()
	res, _ := svc.Save(ctx, baseSaveInput())
	if err := svc.Delete(ctx, res.Memory.ID, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	restored, err := svc.Restore(ctx, res.Memory.ID)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.ID != res.Memory.ID {
		t.Errorf("Restore devolvió id %d, esperaba %d", restored.ID, res.Memory.ID)
	}
	got, _ := obs.Get(ctx, res.Memory.ID)
	if got == nil {
		t.Error("tras Restore la memoria debe volver a recuperarse")
	}
	last := sync.enqueued[len(sync.enqueued)-1]
	if last.Op != domain.SyncOpUpsert {
		t.Errorf("Restore debe encolar una mutación upsert, fue %v", last.Op)
	}
}

func TestRestoreMissing(t *testing.T) {
	svc, _, _ := newMemoryFixture()
	if _, err := svc.Restore(context.Background(), 999); err != domain.ErrNotFound {
		t.Errorf("Restore de id inexistente debe dar ErrNotFound, fue %v", err)
	}
}

func TestRecentDelegates(t *testing.T) {
	svc, _, _ := newMemoryFixture()
	ctx := context.Background()
	_, _ = svc.Save(ctx, baseSaveInput())
	got, err := svc.Recent(ctx, ports.RecentQuery{Project: "proj", Limit: 10})
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("esperaba 1 reciente, hubo %d", len(got))
	}
}

func TestSaveIndexesVector(t *testing.T) {
	obs := newFakeObs()
	sync := &fakeSync{}
	emb := newFakeEmbedder()
	vidx := newFakeVectorIndex()
	svc := NewMemoryService(obs, sync, passthroughUoW{}, fixedClock{now: clockNow}, emb, vidx)

	res, err := svc.Save(context.Background(), baseSaveInput())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, ok := vidx.vecs[res.Memory.ID]; !ok {
		t.Error("Save debe indexar el vector de la memoria nueva")
	}
	if emb.calls != 1 {
		t.Errorf("Save debe llamar al embedder una vez, llamó %d", emb.calls)
	}
}

// Save es TOLERANTE al embebedor caído: la memoria se guarda igual (sin vector), marcando
// EmbeddingDeferred, registrando un aviso y dejándola pendiente de backfill. No se pierde nada.
func TestSaveToleratesEmbedderFailure(t *testing.T) {
	obs := newFakeObs()
	sync := &fakeSync{}
	emb := newFakeEmbedder()
	emb.failWith = errInducedSvc
	vidx := newFakeVectorIndex()
	var warned int
	svc := NewMemoryService(obs, sync, passthroughUoW{}, fixedClock{now: clockNow}, emb, vidx,
		WithWarnLogger(func(string, ...any) { warned++ }))

	res, err := svc.Save(context.Background(), baseSaveInput())
	if err != nil {
		t.Fatalf("Save no debe fallar con el embebedor caído, fue %v", err)
	}
	if res.Outcome != SaveCreated {
		t.Errorf("outcome = %q, se esperaba created", res.Outcome)
	}
	if !res.EmbeddingDeferred {
		t.Error("EmbeddingDeferred debe ser true cuando el embebedor falla")
	}
	if res.Memory.ID == 0 {
		t.Error("la memoria debe haberse guardado (id != 0) pese al fallo del embebedor")
	}
	if got, _ := obs.Get(context.Background(), res.Memory.ID); got == nil {
		t.Error("la memoria debe persistir en el repo aunque no tenga vector")
	}
	if _, ok := vidx.vecs[res.Memory.ID]; ok {
		t.Error("no debe haber vector indexado cuando el embebedor falla")
	}
	if warned == 0 {
		t.Error("se esperaba un aviso registrado por el guardado sin vector")
	}
}

func TestSearchEmbedsAndFilters(t *testing.T) {
	obs := newFakeObs()
	sync := &fakeSync{}
	emb := newFakeEmbedder()
	vidx := newFakeVectorIndex()
	svc := NewMemoryService(obs, sync, passthroughUoW{}, fixedClock{now: clockNow}, emb, vidx)
	ctx := context.Background()

	a := baseSaveInput()
	a.Content = "memoria a"
	if _, err := svc.Save(ctx, a); err != nil {
		t.Fatalf("Save a: %v", err)
	}
	b := baseSaveInput()
	b.Content = "memoria b"
	if _, err := svc.Save(ctx, b); err != nil {
		t.Fatalf("Save b: %v", err)
	}
	other := baseSaveInput()
	other.Project = "otro"
	other.Content = "memoria de otro proyecto"
	if _, err := svc.Save(ctx, other); err != nil {
		t.Fatalf("Save other: %v", err)
	}

	results, err := svc.Search(ctx, ports.SearchQuery{Query: "algo", Project: "proj", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("Search filtrada por 'proj' debe devolver 2, fue %d", len(results))
	}
}

// mutableClock permite avanzar el tiempo entre llamadas en un test.
type mutableClock struct{ now time.Time }

func (c *mutableClock) Now() time.Time { return c.now }

// errInducedSvc fuerza fallos en dependencias dentro de los tests de servicio.
var errInducedSvc = errInducedError("fallo inducido")

type errInducedError string

func (e errInducedError) Error() string { return string(e) }
