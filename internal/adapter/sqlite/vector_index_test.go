package sqlite

import (
	"context"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

// seedMemory inserta una memoria mínima y devuelve su id, para colgarle un vector.
func seedMemory(t *testing.T, db *DB, syncID string) int64 {
	t.Helper()
	repo := NewMemoryRepo(db)
	o := sampleMemory()
	o.SyncID = syncID
	id, err := repo.Add(context.Background(), o)
	if err != nil {
		t.Fatalf("seedMemory: %v", err)
	}
	return id
}

func TestVectorUpsertAndNearestRanking(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	idx := NewVectorIndex(db)
	ctx := context.Background()

	// Tres vectores en el plano. La consulta apunta a (1,0): el más cercano es 'este', luego
	// 'diagonal', y 'opuesto' (apunta a -1,0) el más lejano.
	este := seedMemory(t, db, "este")
	diagonal := seedMemory(t, db, "diagonal")
	opuesto := seedMemory(t, db, "opuesto")

	if err := idx.Upsert(ctx, este, []float32{1, 0}, "fake"); err != nil {
		t.Fatalf("Upsert este: %v", err)
	}
	if err := idx.Upsert(ctx, diagonal, []float32{1, 1}, "fake"); err != nil {
		t.Fatalf("Upsert diagonal: %v", err)
	}
	if err := idx.Upsert(ctx, opuesto, []float32{-1, 0}, "fake"); err != nil {
		t.Fatalf("Upsert opuesto: %v", err)
	}

	got, err := idx.Nearest(ctx, []float32{1, 0}, 3)
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("esperaba 3 vecinos, obtuve %d", len(got))
	}
	if got[0].MemoryID != este || got[1].MemoryID != diagonal || got[2].MemoryID != opuesto {
		t.Errorf("orden por cercanía incorrecto: %+v", got)
	}
	if got[0].Score <= got[1].Score || got[1].Score <= got[2].Score {
		t.Errorf("los scores deben decrecer: %v > %v > %v", got[0].Score, got[1].Score, got[2].Score)
	}
}

func TestVectorUpsertReplaces(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	idx := NewVectorIndex(db)
	ctx := context.Background()

	id := seedMemory(t, db, "m1")
	if err := idx.Upsert(ctx, id, []float32{1, 0}, "fake"); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}
	// Reemplazar el vector no debe duplicar la fila (memory_id es PK).
	if err := idx.Upsert(ctx, id, []float32{0, 1}, "fake"); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}
	got, err := idx.Nearest(ctx, []float32{0, 1}, 10)
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("debe haber un único vector, hay %d", len(got))
	}
	if got[0].Score < 0.99 {
		t.Errorf("tras reemplazar, el vector debe coincidir con la consulta (score~1), fue %v", got[0].Score)
	}
}

func TestVectorDelete(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	idx := NewVectorIndex(db)
	ctx := context.Background()

	id := seedMemory(t, db, "m1")
	_ = idx.Upsert(ctx, id, []float32{1, 0}, "fake")
	if err := idx.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := idx.Nearest(ctx, []float32{1, 0}, 10)
	if len(got) != 0 {
		t.Errorf("tras Delete no debe quedar vector, hay %d", len(got))
	}
}

func TestVectorCascadeOnHardDelete(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	idx := NewVectorIndex(db)
	ctx := context.Background()

	id := seedMemory(t, db, "m1")
	_ = idx.Upsert(ctx, id, []float32{1, 0}, "fake")
	if err := repo.HardDelete(ctx, id); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}
	// La clave foránea ON DELETE CASCADE debe haber quitado el vector.
	got, _ := idx.Nearest(ctx, []float32{1, 0}, 10)
	if len(got) != 0 {
		t.Errorf("el hard-delete de la memoria debe cascadear al vector, quedan %d", len(got))
	}
}

// Aserción de que el tipo de dominio usado existe (evita import sin uso si cambia el sample).
var _ = domain.TypeDecision
