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

func TestPendingEmbedding(t *testing.T) {
	db := newTestDB(t)
	seedSession(t, db, "s1")
	repo := NewMemoryRepo(db)
	idx := NewVectorIndex(db)
	ctx := context.Background()

	// sinVector: nunca se indexa → pendiente.
	sinVector := seedMemory(t, db, "sin-vector")
	// alDia: vector del modelo activo → no pendiente.
	alDia := seedMemory(t, db, "al-dia")
	// obsoleto: vector de otro modelo → pendiente (el modelo es contrato de datos).
	obsoleto := seedMemory(t, db, "obsoleto")
	// borrado: tiene vector del modelo activo pero está soft-deleted → excluido.
	borrado := seedMemory(t, db, "borrado")

	if err := idx.Upsert(ctx, alDia, []float32{1, 0}, "bge-m3"); err != nil {
		t.Fatalf("Upsert alDia: %v", err)
	}
	if err := idx.Upsert(ctx, obsoleto, []float32{1, 0}, "modelo-viejo"); err != nil {
		t.Fatalf("Upsert obsoleto: %v", err)
	}
	if err := idx.Upsert(ctx, borrado, []float32{1, 0}, "bge-m3"); err != nil {
		t.Fatalf("Upsert borrado: %v", err)
	}
	if err := repo.SoftDelete(ctx, borrado); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	ids, err := idx.PendingEmbedding(ctx, "bge-m3", 0)
	if err != nil {
		t.Fatalf("PendingEmbedding: %v", err)
	}
	// Esperados: sinVector y obsoleto, en orden de id ascendente.
	want := []int64{sinVector, obsoleto}
	if len(ids) != len(want) {
		t.Fatalf("pendientes = %v, se esperaban %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("pendientes[%d] = %d, se esperaba %d (lista %v)", i, ids[i], want[i], ids)
		}
	}

	// El límite acota el resultado.
	limited, err := idx.PendingEmbedding(ctx, "bge-m3", 1)
	if err != nil {
		t.Fatalf("PendingEmbedding con límite: %v", err)
	}
	if len(limited) != 1 || limited[0] != sinVector {
		t.Errorf("con límite 1 esperaba [%d], obtuve %v", sinVector, limited)
	}
}

// Aserción de que el tipo de dominio usado existe (evita import sin uso si cambia el sample).
var _ = domain.TypeDecision
