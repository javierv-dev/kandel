package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// baseTime es un instante fijo para que las pruebas sean deterministas.
var baseTime = time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)

// errInduced es un error de prueba para forzar el rollback de una transacción.
var errInduced = errors.New("error inducido")

// newTestDB abre una base temporal ya migrada y la cierra al terminar la prueba.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedSession inserta una sesión para satisfacer la clave foránea de memories.
func seedSession(t *testing.T, db *DB, id string) {
	t.Helper()
	_, err := db.sql.Exec(
		`INSERT INTO sessions (id, project, directory, started_at) VALUES (?, ?, ?, ?)`,
		id, "proj", "/dir", formatTime(baseTime),
	)
	if err != nil {
		t.Fatalf("seedSession: %v", err)
	}
}

// sampleMemory construye una memoria válida con valores por defecto razonables.
func sampleMemory() domain.Memory {
	return domain.Memory{
		SyncID:         "mem-1",
		SessionID:      "s1",
		Project:        "proj",
		Type:           domain.TypeDecision,
		Scope:          domain.ScopeProject,
		Title:          "Usar hexagonal",
		Content:        "Se decide arquitectura hexagonal.",
		NormalizedHash: "hash-1",
		RevisionCount:  1,
		DuplicateCount: 1,
		CreatedAt:      baseTime,
		UpdatedAt:      baseTime,
	}
}

func TestAddAndGet(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	ctx := context.Background()

	in := sampleMemory()
	in.ToolName = "claude"
	in.LastSeenAt = baseTime

	id, err := repo.Add(ctx, in)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id == 0 {
		t.Fatal("Add devolvió id 0")
	}

	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get devolvió nil para una memoria existente")
	}
	if got.Title != in.Title || got.Content != in.Content {
		t.Errorf("título/contenido no coinciden: %q / %q", got.Title, got.Content)
	}
	if got.Type != in.Type || got.Scope != in.Scope {
		t.Errorf("tipo/scope no coinciden: %v / %v", got.Type, got.Scope)
	}
	if got.ToolName != "claude" {
		t.Errorf("tool_name no coincide: %q", got.ToolName)
	}
	if !got.CreatedAt.Equal(baseTime) {
		t.Errorf("timestamps no coinciden: created=%v", got.CreatedAt)
	}
	if got.DeletedAt != nil {
		t.Errorf("memoria recién creada no debe estar borrada")
	}
}

func TestGetMissing(t *testing.T) {
	db := newTestDB(t)
	repo := NewMemoryRepo(db)
	got, err := repo.Get(context.Background(), 999)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("Get de id inexistente debe ser nil, fue %+v", got)
	}
}

func TestSoftDeleteHidesMemory(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	ctx := context.Background()

	id, err := repo.Add(ctx, sampleMemory())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := repo.SoftDelete(ctx, id); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("una memoria con soft-delete no debe recuperarse, fue %+v", got)
	}
}

func TestRestoreUndoesSoftDelete(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	ctx := context.Background()

	id, err := repo.Add(ctx, sampleMemory())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := repo.SoftDelete(ctx, id); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if err := repo.Restore(ctx, id); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Error("tras Restore la memoria debe volver a recuperarse")
	}
	// Restaurar algo que no estaba borrado (ya viva) → ErrNotFound (no afecta filas).
	if err := repo.Restore(ctx, id); err != domain.ErrNotFound {
		t.Errorf("Restore de una memoria viva debe dar ErrNotFound, fue %v", err)
	}
}

func TestFindByHash(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	ctx := context.Background()

	in := sampleMemory()
	in.NormalizedHash = "abc"
	if _, err := repo.Add(ctx, in); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := repo.FindByHash(ctx, "proj", domain.ScopeProject, "abc")
	if err != nil {
		t.Fatalf("FindByHash: %v", err)
	}
	if got == nil {
		t.Fatal("FindByHash debió encontrar la memoria")
	}

	miss, err := repo.FindByHash(ctx, "proj", domain.ScopeProject, "otro")
	if err != nil {
		t.Fatalf("FindByHash: %v", err)
	}
	if miss != nil {
		t.Errorf("FindByHash con hash inexistente debe ser nil")
	}
}

func TestRecentOrderingAndFilters(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	ctx := context.Background()

	first := sampleMemory()
	first.Content = "primera"
	first.CreatedAt = baseTime
	if _, err := repo.Add(ctx, first); err != nil {
		t.Fatalf("Add first: %v", err)
	}
	second := sampleMemory()
	second.SyncID = "mem-2"
	second.Content = "segunda"
	second.CreatedAt = baseTime.Add(time.Hour)
	if _, err := repo.Add(ctx, second); err != nil {
		t.Fatalf("Add second: %v", err)
	}
	// Una memoria de otro proyecto que el filtro debe excluir.
	other := sampleMemory()
	other.SyncID = "mem-3"
	other.Project = "otro"
	if _, err := repo.Add(ctx, other); err != nil {
		t.Fatalf("Add other: %v", err)
	}

	got, err := repo.Recent(ctx, ports.RecentQuery{Project: "proj", Limit: 10})
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("esperaba 2 memorias de 'proj', obtuve %d", len(got))
	}
	if got[0].Content != "segunda" {
		t.Errorf("la más reciente debe ir primero, fue %q", got[0].Content)
	}
}

func TestRecentFiltersByTypesAndResolved(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	ctx := context.Background()

	dec := sampleMemory() // decision, resolution vacía
	if _, err := repo.Add(ctx, dec); err != nil {
		t.Fatalf("Add decision: %v", err)
	}
	disc := sampleMemory()
	disc.SyncID = "mem-disc"
	disc.Type = domain.TypeDiscovery
	if _, err := repo.Add(ctx, disc); err != nil {
		t.Fatalf("Add discovery: %v", err)
	}
	resolved := sampleMemory()
	resolved.SyncID = "mem-res"
	resolved.Resolution = domain.ResolutionResolved
	if _, err := repo.Add(ctx, resolved); err != nil {
		t.Fatalf("Add resolved: %v", err)
	}

	// Solo decisiones, excluyendo resueltas → queda la decisión de resolución vacía.
	got, err := repo.Recent(ctx, ports.RecentQuery{
		Project: "proj", Types: []domain.MemoryType{domain.TypeDecision}, ExcludeResolved: true, Limit: -1,
	})
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 1 || got[0].Type != domain.TypeDecision || got[0].Resolution == domain.ResolutionResolved {
		t.Fatalf("filtro tipo+resuelta mal aplicado: %+v", got)
	}

	// Solo discovery → una.
	gotDisc, _ := repo.Recent(ctx, ports.RecentQuery{Project: "proj", Types: []domain.MemoryType{domain.TypeDiscovery}, Limit: 10})
	if len(gotDisc) != 1 || gotDisc[0].Type != domain.TypeDiscovery {
		t.Fatalf("filtro por discovery mal aplicado: %+v", gotDisc)
	}
}

func TestPendingResolutionExcludesSuperseded(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	ctx := context.Background()

	superseded := sampleMemory() // mem-1, pendiente pero reemplazada
	superseded.Resolution = domain.ResolutionOpen
	if _, err := repo.Add(ctx, superseded); err != nil {
		t.Fatalf("Add superseded: %v", err)
	}
	live := sampleMemory()
	live.SyncID = "mem-live"
	live.Resolution = domain.ResolutionOpen
	if _, err := repo.Add(ctx, live); err != nil {
		t.Fatalf("Add live: %v", err)
	}
	seedSupersede(t, db, "mem-live", "mem-1") // mem-live reemplaza a mem-1

	got, err := repo.PendingResolution(ctx, "proj", 10)
	if err != nil {
		t.Fatalf("PendingResolution: %v", err)
	}
	if len(got) != 1 || got[0].SyncID != "mem-live" {
		t.Fatalf("un pendiente reemplazado no debe listarse: %+v", got)
	}
}

func TestConflictCheckedRoundtripAndSweepQuery(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	ctx := context.Background()

	// Una normativa (decision) nace sin verificar (conflict_checked = 0).
	norm := sampleMemory() // TypeDecision
	idNorm, err := repo.Add(ctx, norm)
	if err != nil {
		t.Fatalf("Add normativa: %v", err)
	}
	got, _ := repo.Get(ctx, idNorm)
	if got == nil || got.ConflictChecked {
		t.Fatalf("una memoria nueva debe nacer sin verificar: %+v", got)
	}

	// Una factual (discovery) no debe aparecer en la cola del barrido aunque esté sin verificar.
	fact := sampleMemory()
	fact.SyncID = "mem-fact"
	fact.Type = domain.TypeDiscovery
	if _, err := repo.Add(ctx, fact); err != nil {
		t.Fatalf("Add factual: %v", err)
	}

	pending, err := repo.UncheckedNormatives(ctx, 10)
	if err != nil {
		t.Fatalf("UncheckedNormatives: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != idNorm {
		t.Fatalf("solo la normativa sin verificar debe estar pendiente: %+v", pending)
	}

	// Marcarla la saca de la cola y persiste el flag.
	if err := repo.SetConflictChecked(ctx, idNorm); err != nil {
		t.Fatalf("SetConflictChecked: %v", err)
	}
	got, _ = repo.Get(ctx, idNorm)
	if got == nil || !got.ConflictChecked {
		t.Errorf("tras marcar debe quedar verificada: %+v", got)
	}
	pending, _ = repo.UncheckedNormatives(ctx, 10)
	if len(pending) != 0 {
		t.Errorf("una normativa verificada no debe seguir pendiente: %+v", pending)
	}
}

// seedSupersede inserta una relación 'supersedes' juzgada: source reemplaza a target.
func seedSupersede(t *testing.T, db *DB, source, target string) {
	t.Helper()
	rel := domain.MemoryRelation{
		SyncID:    "rel-" + source + "-" + target,
		SourceID:  source,
		TargetID:  target,
		Kind:      domain.RelationSupersedes,
		Status:    domain.JudgmentJudged,
		CreatedAt: baseTime,
		UpdatedAt: baseTime,
	}
	if _, err := NewRelationRepo(db).Insert(context.Background(), rel); err != nil {
		t.Fatalf("Insert relación supersede: %v", err)
	}
}

func TestRecentExcludesSuperseded(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	ctx := context.Background()

	old := sampleMemory() // mem-1, será reemplazada (target)
	old.Content = "vieja"
	if _, err := repo.Add(ctx, old); err != nil {
		t.Fatalf("Add old: %v", err)
	}
	current := sampleMemory()
	current.SyncID = "mem-2" // reemplaza a mem-1 (source, más reciente)
	current.Content = "nueva"
	current.CreatedAt = baseTime.Add(time.Hour)
	if _, err := repo.Add(ctx, current); err != nil {
		t.Fatalf("Add current: %v", err)
	}
	seedSupersede(t, db, "mem-2", "mem-1")

	// Sin el flag: navegación cruda ve toda la historia (ambas).
	all, err := repo.Recent(ctx, ports.RecentQuery{Project: "proj", Limit: 10})
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("sin ExcludeSuperseded esperaba 2, obtuve %d", len(all))
	}

	// Con el flag: la reemplazada (mem-1) desaparece.
	got, err := repo.Recent(ctx, ports.RecentQuery{Project: "proj", Limit: 10, ExcludeSuperseded: true})
	if err != nil {
		t.Fatalf("Recent ExcludeSuperseded: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("con ExcludeSuperseded esperaba 1, obtuve %d", len(got))
	}
	if got[0].SyncID != "mem-2" {
		t.Errorf("debe quedar la reemplazante mem-2, quedó %q", got[0].SyncID)
	}
}

func TestSearchExcludesSupersededUnlessIncluded(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	ctx := context.Background()

	old := sampleMemory() // mem-1, target reemplazado
	idOld, _ := repo.Add(ctx, old)
	current := sampleMemory()
	current.SyncID = "mem-2"
	idNew, _ := repo.Add(ctx, current)
	seedSupersede(t, db, "mem-2", "mem-1")

	neighbors := []ports.Neighbor{
		{MemoryID: idNew, Score: 0.9},
		{MemoryID: idOld, Score: 0.8},
	}

	// Por defecto la búsqueda oculta la reemplazada.
	got, err := repo.Search(ctx, ports.SearchQuery{Limit: 10}, neighbors)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Memory.ID != idNew {
		t.Fatalf("por defecto esperaba solo mem-2, obtuve %d resultados", len(got))
	}

	// Con include_superseded vuelve a verse toda la historia.
	withHistory, err := repo.Search(ctx, ports.SearchQuery{Limit: 10, IncludeSuperseded: true}, neighbors)
	if err != nil {
		t.Fatalf("Search IncludeSuperseded: %v", err)
	}
	if len(withHistory) != 2 {
		t.Fatalf("con IncludeSuperseded esperaba 2, obtuve %d", len(withHistory))
	}
}

func TestSearchPreservesNeighborOrderAndScore(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	ctx := context.Background()

	a := sampleMemory()
	a.Content = "a"
	idA, _ := repo.Add(ctx, a)
	b := sampleMemory()
	b.SyncID = "mem-2"
	b.Content = "b"
	idB, _ := repo.Add(ctx, b)

	// Vecinos en orden de cercanía: B primero (score mayor), luego A.
	neighbors := []ports.Neighbor{
		{MemoryID: idB, Score: 0.9},
		{MemoryID: idA, Score: 0.5},
	}
	got, err := repo.Search(ctx, ports.SearchQuery{Limit: 10}, neighbors)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("esperaba 2 resultados, obtuve %d", len(got))
	}
	if got[0].ID != idB || got[0].Score != 0.9 {
		t.Errorf("primer resultado debe ser B con score 0.9, fue id=%d score=%v", got[0].ID, got[0].Score)
	}
	if got[1].ID != idA || got[1].Score != 0.5 {
		t.Errorf("segundo resultado debe ser A con score 0.5, fue id=%d score=%v", got[1].ID, got[1].Score)
	}
}

func TestSearchEmptyNeighbors(t *testing.T) {
	db := newTestDB(t)
	repo := NewMemoryRepo(db)
	got, err := repo.Search(context.Background(), ports.SearchQuery{}, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got != nil {
		t.Errorf("sin vecinos el resultado debe ser nil, fue %+v", got)
	}
}

func TestPendingResolution(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	ctx := context.Background()

	pending := sampleMemory()
	pending.Content = "pendiente"
	pending.Resolution = domain.ResolutionOpen
	if _, err := repo.Add(ctx, pending); err != nil {
		t.Fatalf("Add pending: %v", err)
	}
	normal := sampleMemory()
	normal.SyncID = "mem-2"
	normal.Content = "normal"
	if _, err := repo.Add(ctx, normal); err != nil {
		t.Fatalf("Add normal: %v", err)
	}
	resolved := sampleMemory()
	resolved.SyncID = "mem-3"
	resolved.Content = "resuelta"
	resolved.Resolution = domain.ResolutionResolved
	if _, err := repo.Add(ctx, resolved); err != nil {
		t.Fatalf("Add resolved: %v", err)
	}

	got, err := repo.PendingResolution(ctx, "proj", 10)
	if err != nil {
		t.Fatalf("PendingResolution: %v", err)
	}
	if len(got) != 1 || got[0].Content != "pendiente" {
		t.Errorf("solo la pendiente (open) debe aparecer, obtuve %+v", got)
	}
}

func TestUnitOfWorkCommitAndRollback(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	uow := NewUnitOfWork(db)
	ctx := context.Background()

	// El repo es tx-aware vía context: llamado con el ctx que da uow.Do, escribe en la
	// transacción, así que commit/rollback afectan de verdad a lo insertado.
	addInTx := func(ctx context.Context, syncID string) error {
		o := sampleMemory()
		o.SyncID = syncID
		_, err := repo.Add(ctx, o)
		return err
	}

	// Commit: lo escrito dentro de Do debe persistir.
	if err := uow.Do(ctx, func(ctx context.Context) error { return addInTx(ctx, "mem-commit") }); err != nil {
		t.Fatalf("Do commit: %v", err)
	}
	got, _ := repo.Recent(ctx, ports.RecentQuery{Project: "proj", Limit: 10})
	if len(got) != 1 {
		t.Fatalf("tras commit esperaba 1 memoria, obtuve %d", len(got))
	}

	// Rollback: un error dentro de Do no debe dejar rastro.
	err := uow.Do(ctx, func(ctx context.Context) error {
		if err := addInTx(ctx, "mem-roll"); err != nil {
			return err
		}
		return errInduced
	})
	if !errors.Is(err, errInduced) {
		t.Fatalf("Do debió propagar el error inducido, fue %v", err)
	}
	got, _ = repo.Recent(ctx, ports.RecentQuery{Project: "proj", Limit: 10})
	if len(got) != 1 {
		t.Errorf("tras rollback debe seguir habiendo 1 memoria, obtuve %d", len(got))
	}
}
