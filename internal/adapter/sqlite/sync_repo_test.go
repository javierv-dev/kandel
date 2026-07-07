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
		Entity:     domain.SyncEntityMemory,
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

	enqueue(t, uow, repo, sampleMutation("mem-1"))
	enqueue(t, uow, repo, sampleMutation("mem-2"))

	pending, err := repo.ListPending(ctx, "remote", 0, 10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("esperaba 2 pendientes, obtuve %d", len(pending))
	}
	if pending[0].EntityKey != "mem-1" || pending[1].EntityKey != "mem-2" {
		t.Errorf("orden por seq incorrecto: %+v", pending)
	}
	if string(pending[0].Payload) != `{"sync_id":"mem-1"}` {
		t.Errorf("payload no coincide: %s", pending[0].Payload)
	}
	if pending[0].Entity != domain.SyncEntityMemory || pending[0].Op != domain.SyncOpUpsert {
		t.Errorf("entity/op no coinciden: %+v", pending[0])
	}
	if pending[0].AckedAt != nil {
		t.Error("una mutación pendiente no debe tener acked_at")
	}

	// afterSeq excluye lo ya visto.
	rest, _ := repo.ListPending(ctx, "remote", pending[0].Seq, 10)
	if len(rest) != 1 || rest[0].EntityKey != "mem-2" {
		t.Errorf("afterSeq debió devolver solo mem-2, fue %+v", rest)
	}
}

func TestSyncAck(t *testing.T) {
	db := newTestDB(t)
	repo := NewSyncRepo(db)
	uow := NewUnitOfWork(db)
	ctx := context.Background()

	enqueue(t, uow, repo, sampleMutation("mem-1"))
	enqueue(t, uow, repo, sampleMutation("mem-2"))
	pending, _ := repo.ListPending(ctx, "remote", 0, 10)

	if err := repo.Ack(ctx, "remote", []int64{pending[0].Seq}); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	after, _ := repo.ListPending(ctx, "remote", 0, 10)
	if len(after) != 1 || after[0].EntityKey != "mem-2" {
		t.Errorf("tras Ack solo debe quedar mem-2 pendiente, fue %+v", after)
	}
}

func TestSyncEnqueueRollback(t *testing.T) {
	db := newTestDB(t)
	repo := NewSyncRepo(db)
	uow := NewUnitOfWork(db)
	ctx := context.Background()

	err := uow.Do(ctx, func(ctx context.Context) error {
		if err := repo.EnqueueMutation(ctx, sampleMutation("mem-roll")); err != nil {
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

func TestSyncCursor(t *testing.T) {
	db := newTestDB(t)
	repo := NewSyncRepo(db)
	ctx := context.Background()

	// Un destino sin cursor arranca en 0 (arranque en frío).
	got, err := repo.GetCursor(ctx, "remote")
	if err != nil {
		t.Fatalf("GetCursor inicial: %v", err)
	}
	if got != 0 {
		t.Errorf("un destino nuevo debe tener cursor 0, fue %d", got)
	}

	// SetCursor guarda y GetCursor lo devuelve.
	if err := repo.SetCursor(ctx, "remote", 42); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}
	if got, _ := repo.GetCursor(ctx, "remote"); got != 42 {
		t.Errorf("cursor esperado 42, fue %d", got)
	}

	// SetCursor de nuevo sobreescribe (upsert, no inserta duplicado).
	if err := repo.SetCursor(ctx, "remote", 100); err != nil {
		t.Fatalf("SetCursor upsert: %v", err)
	}
	if got, _ := repo.GetCursor(ctx, "remote"); got != 100 {
		t.Errorf("cursor esperado 100 tras upsert, fue %d", got)
	}

	// Los destinos son independientes.
	if got, _ := repo.GetCursor(ctx, "otro"); got != 0 {
		t.Errorf("otro destino debe seguir en 0, fue %d", got)
	}
}

func TestSyncConflicts(t *testing.T) {
	db := newTestDB(t)
	repo := NewSyncRepo(db)
	ctx := context.Background()

	c := domain.SyncConflict{
		SyncID: "j1", Project: "proj", IncomingOp: domain.SyncOpUpsert,
		Incoming: []byte(`{"title":"remota"}`), IncomingAt: baseTime,
	}
	if err := repo.RecordConflict(ctx, c); err != nil {
		t.Fatalf("RecordConflict: %v", err)
	}
	// Upsert por sync_id: registrar otra vez la misma key reemplaza, no duplica.
	c2 := c
	c2.Incoming = []byte(`{"title":"remota-v2"}`)
	if err := repo.RecordConflict(ctx, c2); err != nil {
		t.Fatalf("RecordConflict upsert: %v", err)
	}
	repo.RecordConflict(ctx, domain.SyncConflict{SyncID: "m2", Project: "otro", IncomingOp: domain.SyncOpDelete, Incoming: []byte(`{}`), IncomingAt: baseTime})

	if n, _ := repo.CountConflicts(ctx, ""); n != 2 {
		t.Errorf("deben quedar 2 conflictos, hay %d", n)
	}
	if n, _ := repo.CountConflicts(ctx, "proj"); n != 1 {
		t.Errorf("el filtro por proyecto debe dar 1, dio %d", n)
	}

	got, err := repo.GetConflict(ctx, "j1")
	if err != nil {
		t.Fatalf("GetConflict: %v", err)
	}
	if got == nil || string(got.Incoming) != `{"title":"remota-v2"}` || got.IncomingOp != domain.SyncOpUpsert {
		t.Errorf("el conflicto debe reflejar el upsert más nuevo: %+v", got)
	}

	list, _ := repo.ListConflicts(ctx, "proj")
	if len(list) != 1 || list[0].SyncID != "j1" {
		t.Errorf("ListConflicts(proj) inesperado: %+v", list)
	}

	if err := repo.DeleteConflict(ctx, "j1"); err != nil {
		t.Fatalf("DeleteConflict: %v", err)
	}
	if got, _ := repo.GetConflict(ctx, "j1"); got != nil {
		t.Errorf("el conflicto borrado no debe seguir: %+v", got)
	}
	if n, _ := repo.CountConflicts(ctx, ""); n != 1 {
		t.Errorf("tras borrar debe quedar 1, hay %d", n)
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
