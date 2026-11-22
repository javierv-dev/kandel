package service

import (
	"context"
	"errors"
	"math"
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

func TestSaveJournalSkipsDedupe(t *testing.T) {
	// La bitácora (journal) está exenta del dedupe: dos entradas idénticas seguidas son DOS filas,
	// no un duplicado contado. Es un log append-only.
	svc, obs, _ := newMemoryFixture()
	ctx := context.Background()
	in := SaveInput{
		Project: "Proj",
		Type:    domain.TypeJournal,
		Title:   "2026-07-06 — avance",
		Content: "Se hizo lo mismo dos veces.",
	}
	if _, err := svc.Save(ctx, in); err != nil {
		t.Fatalf("Save 1: %v", err)
	}
	res, err := svc.Save(ctx, in)
	if err != nil {
		t.Fatalf("Save 2: %v", err)
	}
	if res.Outcome != SaveCreated {
		t.Errorf("una journal idéntica debe crear fila nueva, fue %v", res.Outcome)
	}
	if len(obs.items) != 2 {
		t.Errorf("dos journals idénticas deben ser 2 filas, hay %d", len(obs.items))
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

// Una memoria global se desancla del proyecto que llegue: cruza todos, no queda pegada a uno.
func TestSaveGlobalScopeDetachesProject(t *testing.T) {
	svc, _, _ := newMemoryFixture()
	in := baseSaveInput()
	in.Project = "kandel"
	in.Scope = domain.ScopeGlobal
	res, err := svc.Save(context.Background(), in)
	if err != nil {
		t.Fatalf("Save global: %v", err)
	}
	if res.Memory.Project != "" {
		t.Errorf("una global debe guardarse sin proyecto (desanclada), fue %q", res.Memory.Project)
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
	// F3: cada actualización incrementa revision_count (nace en 1 tras Save).
	if updated.RevisionCount != res.Memory.RevisionCount+1 {
		t.Errorf("revision_count debe incrementarse: era %d, es %d", res.Memory.RevisionCount, updated.RevisionCount)
	}
	// Save + Update encolan una mutación cada uno.
	if len(sync.enqueued) != 2 {
		t.Errorf("esperaba 2 mutaciones, hubo %d", len(sync.enqueued))
	}
}

// Editar el contenido o el título fija la línea base de la telemetría (así se mide la versión nueva);
// editar solo metadatos que no cambian lo inyectado, o "editar" con el mismo texto, no la toca.
func TestUpdateRebasaLaTelemetriaSoloSiCambiaLoInyectado(t *testing.T) {
	obs, sync := newFakeObs(), &fakeSync{}
	usage := &fakeUsageRepo{}
	svc := NewMemoryService(obs, sync, passthroughUoW{}, fixedClock{now: clockNow}, newFakeEmbedder(), newFakeVectorIndex(),
		WithUsageRebaser(usage))
	ctx := context.Background()
	res, _ := svc.Save(ctx, baseSaveInput())
	id := res.Memory.ID

	// Mismo texto: no cambia nada de lo que se inyecta.
	same := res.Memory.Content
	sameTitle := res.Memory.Title
	if _, err := svc.Update(ctx, id, UpdateInput{Content: &same, Title: &sameTitle}); err != nil {
		t.Fatalf("Update mismo texto: %v", err)
	}
	// Solo metadatos.
	typ := domain.TypeDiscovery
	if _, err := svc.Update(ctx, id, UpdateInput{Type: &typ}); err != nil {
		t.Fatalf("Update tipo: %v", err)
	}
	if len(usage.rebased) != 0 {
		t.Fatalf("no debía fijarse línea base sin cambio de texto, se fijó en %v", usage.rebased)
	}

	// Contenido distinto: sí.
	newContent := "contenido reescrito"
	if _, err := svc.Update(ctx, id, UpdateInput{Content: &newContent}); err != nil {
		t.Fatalf("Update contenido: %v", err)
	}
	if len(usage.rebased) != 1 || usage.rebased[0] != id || !usage.rebasedAt.Equal(clockNow) {
		t.Fatalf("tras editar el contenido debía fijarse la línea base de %d a %v, fue %v en %v", id, clockNow, usage.rebased, usage.rebasedAt)
	}

	// Título distinto: sí.
	newTitle := "título reescrito"
	if _, err := svc.Update(ctx, id, UpdateInput{Title: &newTitle}); err != nil {
		t.Fatalf("Update título: %v", err)
	}
	if len(usage.rebased) != 2 {
		t.Errorf("tras editar el título debía fijarse otra vez, rebased=%v", usage.rebased)
	}
}

// TestUpdateReembedsOnContentChange cubre F2: al cambiar el contenido, Update recalcula el vector
// (antes lo dejaba obsoleto → mem_search matcheaba el texto viejo).
func TestUpdateReembedsOnContentChange(t *testing.T) {
	obs, sync := newFakeObs(), &fakeSync{}
	emb, vecs := newFakeEmbedder(), newFakeVectorIndex()
	svc := NewMemoryService(obs, sync, passthroughUoW{}, fixedClock{now: clockNow}, emb, vecs)
	ctx := context.Background()
	res, _ := svc.Save(ctx, baseSaveInput())

	callsAfterSave := emb.calls
	emb.vec = []float32{0, 1, 0} // el nuevo contenido embebe distinto
	newContent := "contenido totalmente distinto"
	if _, err := svc.Update(ctx, res.Memory.ID, UpdateInput{Content: &newContent}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if emb.calls <= callsAfterSave {
		t.Error("Update con contenido nuevo debe re-embeber (no lo hizo)")
	}
	if got := vecs.vecs[res.Memory.ID]; len(got) != 3 || got[1] != 1 {
		t.Errorf("el vector debe reflejar el contenido nuevo, fue %v", got)
	}
}

// TestUpdateDropsStaleVectorWhenEmbedderDown cubre F2 en el camino degradado: si el embebedor está
// caído al re-embeber, el vector obsoleto se BORRA (queda pendiente de backfill) en vez de servir
// búsquedas contra el texto viejo. El Update en sí no falla.
func TestUpdateDropsStaleVectorWhenEmbedderDown(t *testing.T) {
	obs, sync := newFakeObs(), &fakeSync{}
	emb, vecs := newFakeEmbedder(), newFakeVectorIndex()
	svc := NewMemoryService(obs, sync, passthroughUoW{}, fixedClock{now: clockNow}, emb, vecs)
	ctx := context.Background()
	res, _ := svc.Save(ctx, baseSaveInput())
	if _, ok := vecs.vecs[res.Memory.ID]; !ok {
		t.Fatal("precondición: la memoria debe tener vector tras Save")
	}

	emb.failWith = errors.New("ollama caído")
	newContent := "contenido nuevo con embebedor caído"
	if _, err := svc.Update(ctx, res.Memory.ID, UpdateInput{Content: &newContent}); err != nil {
		t.Fatalf("Update no debe fallar por el embebedor caído: %v", err)
	}
	if _, ok := vecs.vecs[res.Memory.ID]; ok {
		t.Error("con el embebedor caído el vector obsoleto debe borrarse (quedar pendiente de backfill)")
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

	if err := svc.Delete(ctx, res.Memory.ID); err != nil {
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
	err := svc.Delete(context.Background(), 999)
	if err != domain.ErrNotFound {
		t.Errorf("Delete de id inexistente debe dar ErrNotFound, fue %v", err)
	}
}

func TestRestoreReindexesAndEnqueuesUpsert(t *testing.T) {
	svc, obs, sync := newMemoryFixture()
	ctx := context.Background()
	res, _ := svc.Save(ctx, baseSaveInput())
	if err := svc.Delete(ctx, res.Memory.ID); err != nil {
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

// Con WithTitleVectors activo, Save indexa el vector del título en su índice paralelo además del de
// contenido, y lo hace en UNA sola llamada al embebedor (EmbedBatch de [contenido, título]).
func TestSaveIndexesTitleVector(t *testing.T) {
	obs := newFakeObs()
	sync := &fakeSync{}
	emb := newFakeEmbedder()
	vidx := newFakeVectorIndex()
	tidx := newFakeVectorIndex()
	svc := NewMemoryService(obs, sync, passthroughUoW{}, fixedClock{now: clockNow}, emb, vidx,
		WithTitleVectors(tidx))

	res, err := svc.Save(context.Background(), baseSaveInput())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, ok := vidx.vecs[res.Memory.ID]; !ok {
		t.Error("Save debe indexar el vector de contenido")
	}
	if _, ok := tidx.vecs[res.Memory.ID]; !ok {
		t.Error("Save debe indexar el vector del título en su índice paralelo")
	}
	// Una sola llamada al embebedor: EmbedBatch cubre contenido y título en un viaje.
	if emb.calls != 1 {
		t.Errorf("Save debe llamar al embebedor una vez (EmbedBatch), llamó %d", emb.calls)
	}
}

// Con WithTitleVectors, si el embebedor está caído no se indexa NINGÚN vector (ni contenido ni
// título) y la memoria queda pendiente de backfill.
func TestSaveTitleVectorToleratesEmbedderFailure(t *testing.T) {
	obs := newFakeObs()
	sync := &fakeSync{}
	emb := newFakeEmbedder()
	emb.failWith = errInducedSvc
	vidx := newFakeVectorIndex()
	tidx := newFakeVectorIndex()
	svc := NewMemoryService(obs, sync, passthroughUoW{}, fixedClock{now: clockNow}, emb, vidx,
		WithTitleVectors(tidx), WithWarnLogger(func(string, ...any) {}))

	res, err := svc.Save(context.Background(), baseSaveInput())
	if err != nil {
		t.Fatalf("Save no debe fallar con el embebedor caído: %v", err)
	}
	if !res.EmbeddingDeferred {
		t.Error("EmbeddingDeferred debe ser true cuando el embebedor falla")
	}
	if _, ok := tidx.vecs[res.Memory.ID]; ok {
		t.Error("no debe haber vector de título cuando el embebedor falla")
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

func TestJournalRecencyBoost(t *testing.T) {
	// Fresca → boost máximo; a una vida media → la mitad; muy vieja → ~0; negativa → máximo.
	if got := journalRecencyBoost(0); got != journalRecencyMaxBoost {
		t.Errorf("boost(0) = %v, se esperaba %v", got, journalRecencyMaxBoost)
	}
	if got := journalRecencyBoost(journalRecencyHalfLife); math.Abs(got-journalRecencyMaxBoost/2) > 1e-9 {
		t.Errorf("boost(vida media) = %v, se esperaba %v", got, journalRecencyMaxBoost/2)
	}
	if got := journalRecencyBoost(100 * journalRecencyHalfLife); got > 1e-6 {
		t.Errorf("boost de una entrada muy vieja debe tender a 0, fue %v", got)
	}
	if got := journalRecencyBoost(-time.Hour); got != journalRecencyMaxBoost {
		t.Errorf("boost de antigüedad negativa se acota al máximo, fue %v", got)
	}
}

func TestSearchJournalRecencyRerank(t *testing.T) {
	// Dos journals: la vieja puntúa MÁS por coseno, pero la reciente gana tras el boost de recencia.
	obs, sync := newFakeObs(), &fakeSync{}
	clk := &mutableClock{now: clockNow}
	vidx := &fakeScoredIndex{scores: map[int64]float64{}}
	svc := NewMemoryService(obs, sync, passthroughUoW{}, clk, newFakeEmbedder(), vidx)
	ctx := context.Background()

	older, err := svc.Save(ctx, SaveInput{Project: "Proj", Type: domain.TypeJournal, Title: "vieja", Content: "entrada vieja"})
	if err != nil {
		t.Fatalf("Save vieja: %v", err)
	}
	clk.now = clockNow.Add(30 * 24 * time.Hour) // la reciente se guarda 30 días después
	newer, err := svc.Save(ctx, SaveInput{Project: "Proj", Type: domain.TypeJournal, Title: "reciente", Content: "entrada reciente"})
	if err != nil {
		t.Fatalf("Save reciente: %v", err)
	}
	// La vieja gana por coseno puro (0.80 > 0.78); el boost de recencia debe darle la vuelta.
	vidx.scores[older.Memory.ID] = 0.80
	vidx.scores[newer.Memory.ID] = 0.78

	results, err := svc.Search(ctx, ports.SearchQuery{Query: "algo", Project: "proj", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("se esperaban 2 resultados, fueron %d", len(results))
	}
	if results[0].Memory.ID != newer.Memory.ID {
		t.Errorf("la journal reciente debe rankear primero tras el boost, fue id=%d", results[0].Memory.ID)
	}
	// El score reportado NO se muta: sigue siendo el coseno puro (el boost solo reordena).
	if results[0].Score != 0.78 {
		t.Errorf("el boost no debe mutar el score reportado, fue %v", results[0].Score)
	}
}

// mutableClock permite avanzar el tiempo entre llamadas en un test.
type mutableClock struct{ now time.Time }

func (c *mutableClock) Now() time.Time { return c.now }

// errInducedSvc fuerza fallos en dependencias dentro de los tests de servicio.
var errInducedSvc = errInducedError("fallo inducido")

type errInducedError string

func (e errInducedError) Error() string { return string(e) }

// TestSearchUsesQuerySide fija que la búsqueda embeba por el lado CONSULTA y no por el de
// documento. No es un detalle de estilo: con modelos asimétricos (qwen3-embedding) el adaptador
// antepone una instrucción solo en EmbedQuery, y sin ella el ruido solapa con los aciertos, de
// modo que el umbral de la inyección por relevancia deja de separar nada. Si alguien revierte
// esta llamada a Embed, la búsqueda sigue devolviendo resultados y el fallo pasa desapercibido:
// por eso se ancla en un test.
func TestSearchUsesQuerySide(t *testing.T) {
	obs, sync := newFakeObs(), &fakeSync{}
	emb, vecs := newFakeEmbedder(), newFakeVectorIndex()
	svc := NewMemoryService(obs, sync, passthroughUoW{}, fixedClock{now: clockNow}, emb, vecs)
	ctx := context.Background()
	if _, err := svc.Save(ctx, baseSaveInput()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Save embebe por el lado documento: no debe haber tocado el lado consulta.
	if len(emb.queries) != 0 {
		t.Fatalf("guardar no debe usar EmbedQuery, usó: %v", emb.queries)
	}

	if _, err := svc.Search(ctx, ports.SearchQuery{Query: "una consulta cualquiera", Limit: 5}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(emb.queries) != 1 {
		t.Fatalf("Search debe embeber por EmbedQuery exactamente una vez, hubo %d", len(emb.queries))
	}
	if emb.queries[0] != "una consulta cualquiera" {
		t.Errorf("la consulta debe llegar íntegra al embebedor, llegó %q", emb.queries[0])
	}
}

// newSupersedesFixture arma el servicio con el repositorio de relaciones cableado (lo que habilita
// el parámetro supersedes) y devuelve también ese repositorio para poder afirmar sobre él.
func newSupersedesFixture() (*MemoryService, *fakeObs, *fakeRelations) {
	obs := newFakeObs()
	rels := &fakeRelations{}
	svc := NewMemoryService(obs, &fakeSync{}, passthroughUoW{}, fixedClock{now: clockNow},
		newFakeEmbedder(), newFakeVectorIndex(), WithRelations(rels))
	return svc, obs, rels
}

// El caso central de 1.3: guardar declarando el reemplazo crea la relación en el mismo Save, sin que
// nadie tenga que editar la memoria vieja a mano después.
func TestSaveConSupersedesCreaLaRelacion(t *testing.T) {
	svc, _, rels := newSupersedesFixture()
	ctx := context.Background()

	vieja, err := svc.Save(ctx, baseSaveInput())
	if err != nil {
		t.Fatalf("Save de la vieja: %v", err)
	}

	in := baseSaveInput()
	in.Title = "Usar hexagonal, con matices"
	in.Content = "Se mantiene hexagonal pero el adaptador HTTP no pasa por el servicio."
	in.Supersedes = vieja.Memory.ID
	nueva, err := svc.Save(ctx, in)
	if err != nil {
		t.Fatalf("Save con supersedes: %v", err)
	}

	if len(rels.items) != 1 {
		t.Fatalf("se esperaba 1 relación, hubo %d", len(rels.items))
	}
	rel := rels.items[0]
	if rel.Kind != domain.RelationSupersedes {
		t.Errorf("relación = %q, se esperaba supersedes", rel.Kind)
	}
	if rel.SourceID != nueva.Memory.SyncID || rel.TargetID != vieja.Memory.SyncID {
		t.Errorf("dirección incorrecta: la nueva (%s) debe reemplazar a la vieja (%s), fue %s→%s",
			nueva.Memory.SyncID, vieja.Memory.SyncID, rel.SourceID, rel.TargetID)
	}
	// Nace juzgada: es una aserción de quien guarda, no una sospecha pendiente de confirmar. Si
	// quedara pendiente, la memoria vieja seguiría apareciendo hasta que alguien la juzgara.
	if rel.Status != domain.JudgmentJudged {
		t.Errorf("estado = %q, se esperaba judged para que la vieja deje de aparecer al instante", rel.Status)
	}
}

// Un id de reemplazo inexistente no debe guardar la memoria nueva a medias: si se guardara y la
// relación no, quien pidió reemplazar creería que la vieja ya no aparece.
func TestSaveConSupersedesInexistenteNoGuarda(t *testing.T) {
	svc, obs, rels := newSupersedesFixture()

	in := baseSaveInput()
	in.Supersedes = 4242
	if _, err := svc.Save(context.Background(), in); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("se esperaba ErrNotFound, fue %v", err)
	}
	if len(obs.items) != 0 {
		t.Errorf("no debía guardarse ninguna memoria, hay %d", len(obs.items))
	}
	if len(rels.items) != 0 {
		t.Errorf("no debía crearse ninguna relación, hay %d", len(rels.items))
	}
}

// Sin el repositorio de relaciones cableado, pedir un reemplazo falla en vez de ignorarse: un
// supersedes que no ocurre y no avisa es peor que no tenerlo.
func TestSaveConSupersedesSinRelacionesFalla(t *testing.T) {
	svc, _, _ := newMemoryFixture()

	in := baseSaveInput()
	in.Supersedes = 1
	if _, err := svc.Save(context.Background(), in); err == nil {
		t.Error("se esperaba un error al pedir supersedes sin repositorio de relaciones")
	}
}

// Reguardar contenido idéntico dispara el dedupe y devuelve la MISMA memoria. Si además se pide que
// se reemplace a sí misma, aceptarlo la ocultaría por completo: no quedaría nada visible.
func TestSaveConSupersedesASiMismaFalla(t *testing.T) {
	svc, _, rels := newSupersedesFixture()
	ctx := context.Background()

	primera, err := svc.Save(ctx, baseSaveInput())
	if err != nil {
		t.Fatalf("Save inicial: %v", err)
	}

	in := baseSaveInput()
	in.Supersedes = primera.Memory.ID
	if _, err := svc.Save(ctx, in); !errors.Is(err, domain.ErrSelfSupersede) {
		t.Fatalf("se esperaba ErrSelfSupersede, fue %v", err)
	}
	if len(rels.items) != 0 {
		t.Errorf("no debía crearse la relación, hay %d", len(rels.items))
	}
}

// Sin supersedes (el caso normal) no se toca el repositorio de relaciones.
func TestSaveSinSupersedesNoCreaRelacion(t *testing.T) {
	svc, _, rels := newSupersedesFixture()
	if _, err := svc.Save(context.Background(), baseSaveInput()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(rels.items) != 0 {
		t.Errorf("sin supersedes no debía crearse ninguna relación, hay %d", len(rels.items))
	}
}

func TestSaveMultiProjectCanonicalNormalizationAndDedupe(t *testing.T) {
	svc, obs, _ := newMemoryFixture()
	ctx := context.Background()

	in := baseSaveInput()
	in.Project = " Yaku-Web , yaku-servidor , yaku-web "
	res1, err := svc.Save(ctx, in)
	if err != nil {
		t.Fatalf("Save 1: %v", err)
	}
	if res1.Memory.Project != "yaku-servidor,yaku-web" {
		t.Errorf("project canónico = %q, se esperaba yaku-servidor,yaku-web", res1.Memory.Project)
	}

	// El segundo guardado llega con el orden invertido: debe colapsar al mismo string canónico y
	// disparar el dedupe exacto dentro de la ventana.
	in2 := baseSaveInput()
	in2.Project = "yaku-servidor, yaku-web"
	res2, err := svc.Save(ctx, in2)
	if err != nil {
		t.Fatalf("Save 2: %v", err)
	}
	if res2.Outcome != SaveDuplicate {
		t.Errorf("el segundo guardado con orden inverso debe ser duplicate, fue %v", res2.Outcome)
	}
	if len(obs.items) != 1 {
		t.Errorf("debe existir 1 sola memoria, hay %d", len(obs.items))
	}
}

func TestUpdateProjectCoupledAndScopeValidation(t *testing.T) {
	svc, _, _ := newMemoryFixture()
	ctx := context.Background()

	res, err := svc.Save(ctx, baseSaveInput())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	coupled := "yaku-web, yaku-servidor"
	updated, err := svc.Update(ctx, res.Memory.ID, UpdateInput{Project: &coupled})
	if err != nil {
		t.Fatalf("Update coupled project: %v", err)
	}
	if updated.Project != "yaku-servidor,yaku-web" {
		t.Errorf("Project tras Update = %q, se esperaba yaku-servidor,yaku-web", updated.Project)
	}

	// Vaciar el proyecto mientras sigue en scope=project debe fallar con ErrAmbiguousProject.
	empty := " , "
	if _, err := svc.Update(ctx, res.Memory.ID, UpdateInput{Project: &empty}); err != domain.ErrAmbiguousProject {
		t.Errorf("Update con project vacío en scope=project debe dar ErrAmbiguousProject, fue %v", err)
	}

	// Pasar a scope=global desancla el proyecto.
	glob := domain.ScopeGlobal
	updatedGlob, err := svc.Update(ctx, res.Memory.ID, UpdateInput{Scope: &glob})
	if err != nil {
		t.Fatalf("Update a global: %v", err)
	}
	if updatedGlob.Project != "" {
		t.Errorf("al pasar a scope=global el proyecto debe limpiarse, quedó %q", updatedGlob.Project)
	}
}
