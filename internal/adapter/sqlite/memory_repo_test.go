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
	in.ReviewAfter = baseTime.AddDate(0, 6, 0)

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
	if !got.CreatedAt.Equal(baseTime) || !got.ReviewAfter.Equal(in.ReviewAfter) {
		t.Errorf("timestamps no coinciden: created=%v review=%v", got.CreatedAt, got.ReviewAfter)
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

func TestNeedingReview(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	ctx := context.Background()

	due := sampleMemory()
	due.Content = "vencida"
	due.ReviewAfter = time.Now().UTC().Add(-time.Hour)
	if _, err := repo.Add(ctx, due); err != nil {
		t.Fatalf("Add due: %v", err)
	}
	future := sampleMemory()
	future.SyncID = "mem-2"
	future.Content = "futura"
	future.ReviewAfter = time.Now().UTC().Add(time.Hour)
	if _, err := repo.Add(ctx, future); err != nil {
		t.Fatalf("Add future: %v", err)
	}

	got, err := repo.NeedingReview(ctx, "proj", 10)
	if err != nil {
		t.Fatalf("NeedingReview: %v", err)
	}
	if len(got) != 1 || got[0].Content != "vencida" {
		t.Errorf("solo la vencida debe necesitar revisión, obtuve %+v", got)
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
