package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
)

func sampleMutation(entityKey string) domain.SyncMutation {
	return domain.SyncMutation{
		TargetKey:  "remote",
		Entity:     domain.SyncEntityObservation,
		EntityKey:  entityKey,
		Op:         domain.SyncOpUpsert,
		Payload:    []byte(`{"sync_id":"` + entityKey + `"}`),
		Source:     "local",
		Project:    "proj",
		OccurredAt: baseTime,
	}
}

// enqueue encola una mutación dentro de una transacción de la unidad de trabajo.
func enqueue(t *testing.T, uow *UnitOfWork, repo *SyncRepo, m domain.SyncMutation) {
	t.Helper()
	err := uow.Do(context.Background(), func(ctx context.Context) error {
		return repo.EnqueueMutation(ctx, m)
	})
	if err != nil {
		t.Fatalf("EnqueueMutation: %v", err)
	}
}

func TestSyncEnqueueAndListPending(t *testing.T) {
	db := newTestDB(t)
	repo := NewSyncRepo(db)
	uow := NewUnitOfWork(db)
	ctx := context.Background()

	enqueue(t, uow, repo, sampleMutation("obs-1"))
	enqueue(t, uow, repo, sampleMutation("obs-2"))

	pending, err := repo.ListPending(ctx, "remote", 0, 10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("esperaba 2 pendientes, obtuve %d", len(pending))
	}
	if pending[0].EntityKey != "obs-1" || pending[1].EntityKey != "obs-2" {
		t.Errorf("orden por seq incorrecto: %+v", pending)
	}
	if string(pending[0].Payload) != `{"sync_id":"obs-1"}` {
		t.Errorf("payload no coincide: %s", pending[0].Payload)
	}
	if pending[0].Entity != domain.SyncEntityObservation || pending[0].Op != domain.SyncOpUpsert {
		t.Errorf("entity/op no coinciden: %+v", pending[0])
	}
	if pending[0].AckedAt != nil {
		t.Error("una mutación pendiente no debe tener acked_at")
	}

	// afterSeq excluye lo ya visto.
	rest, _ := repo.ListPending(ctx, "remote", pending[0].Seq, 10)
	if len(rest) != 1 || rest[0].EntityKey != "obs-2" {
		t.Errorf("afterSeq debió devolver solo obs-2, fue %+v", rest)
	}
}

func TestSyncAck(t *testing.T) {
	db := newTestDB(t)
	repo := NewSyncRepo(db)
	uow := NewUnitOfWork(db)
	ctx := context.Background()

	enqueue(t, uow, repo, sampleMutation("obs-1"))
	enqueue(t, uow, repo, sampleMutation("obs-2"))
	pending, _ := repo.ListPending(ctx, "remote", 0, 10)

	if err := repo.Ack(ctx, "remote", []int64{pending[0].Seq}); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	after, _ := repo.ListPending(ctx, "remote", 0, 10)
	if len(after) != 1 || after[0].EntityKey != "obs-2" {
		t.Errorf("tras Ack solo debe quedar obs-2 pendiente, fue %+v", after)
	}
}

func TestSyncEnqueueRollback(t *testing.T) {
	db := newTestDB(t)
	repo := NewSyncRepo(db)
	uow := NewUnitOfWork(db)
	ctx := context.Background()

	err := uow.Do(ctx, func(ctx context.Context) error {
		if err := repo.EnqueueMutation(ctx, sampleMutation("obs-roll")); err != nil {
			return err
		}
		return errInduced
	})
	if err != errInduced {
		t.Fatalf("Do debió propagar el error inducido, fue %v", err)
	}
	pending, _ := repo.ListPending(ctx, "remote", 0, 10)
	if len(pending) != 0 {
		t.Errorf("tras rollback no debe haber mutaciones, hay %d", len(pending))
	}
}

func TestSyncChunks(t *testing.T) {
	db := newTestDB(t)
	repo := NewSyncRepo(db)
	ctx := context.Background()

	if err := repo.RecordChunk(ctx, "remote", "chunk-a"); err != nil {
		t.Fatalf("RecordChunk: %v", err)
	}
	// Idempotente: registrar dos veces no falla.
	if err := repo.RecordChunk(ctx, "remote", "chunk-a"); err != nil {
		t.Fatalf("RecordChunk repetido: %v", err)
	}
	_ = repo.RecordChunk(ctx, "remote", "chunk-b")
	_ = repo.RecordChunk(ctx, "otro", "chunk-c")

	got, err := repo.SyncedChunks(ctx, "remote")
	if err != nil {
		t.Fatalf("SyncedChunks: %v", err)
	}
	if len(got) != 2 || !got["chunk-a"] || !got["chunk-b"] {
		t.Errorf("chunks de 'remote' incorrectos: %+v", got)
	}
}

func TestSyncLease(t *testing.T) {
	db := newTestDB(t)
	repo := NewSyncRepo(db)
	ctx := context.Background()

	ok, err := repo.AcquireLease(ctx, "remote", "owner-1", time.Minute)
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	if !ok {
		t.Fatal("el primer AcquireLease debe conceder el lease")
	}

	// Otro owner no puede tomarlo mientras siga vigente.
	ok, _ = repo.AcquireLease(ctx, "remote", "owner-2", time.Minute)
	if ok {
		t.Error("otro owner no debe poder tomar un lease vigente")
	}

	// El mismo owner puede renovarlo.
	ok, _ = repo.AcquireLease(ctx, "remote", "owner-1", time.Minute)
	if !ok {
		t.Error("el mismo owner debe poder renovar su lease")
	}

	// Tras liberar, otro owner ya puede tomarlo.
	if err := repo.ReleaseLease(ctx, "remote", "owner-1"); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	ok, _ = repo.AcquireLease(ctx, "remote", "owner-2", time.Minute)
	if !ok {
		t.Error("tras liberar, otro owner debe poder tomar el lease")
	}
}
