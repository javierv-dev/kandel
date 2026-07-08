package service_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/javierv-dev/kandel/internal/adapter/sqlite"
	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/service"
)

// tokenCapturingClient es un ports.SyncClient mínimo que registra el token con el que se le llama y
// cuántas filas recibió. Vive en el paquete _test externo para no chocar con los fakes internos.
type tokenCapturingClient struct {
	pushedToken string
	pushedRows  int
}

func (c *tokenCapturingClient) Push(_ context.Context, _, token string, changes []ports.Change) error {
	c.pushedToken = token
	c.pushedRows += len(changes)
	return nil
}

func (c *tokenCapturingClient) Pull(_ context.Context, _, _ string, cursor int64) ([]ports.Change, int64, error) {
	return nil, cursor, nil
}

// TestPushDrainsQueueWhenHubTokenDiffersFromQueueTarget es la regresión del bug de conflación
// (memoria id 201): los productores encolan con TargetKey="remote" (defaultSyncTarget), pero el token
// del hub puede ser cualquier secreto (aquí "cadena1234"). Antes del fix, Push usaba el token como
// clave de cola y ListPending del SQLite real (WHERE target_key=?) no encontraba nada → Push no-op
// silencioso. El test usa el repo SQLite REAL a propósito: el fake de sync_test.go ignora el argumento
// target y por eso el bug pasaba verde. Aquí la memoria no existe, así que Push manda un tombstone; da
// igual, lo que se verifica es que la cola se drena y la fila viaja bajo el token del hub.
func TestPushDrainsQueueWhenHubTokenDiffersFromQueueTarget(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	syncRepo := sqlite.NewSyncRepo(db)
	memoryRepo := sqlite.NewMemoryRepo(db)
	uow := sqlite.NewUnitOfWork(db)

	// Encola una mutación tal como lo hacen los productores: TargetKey = defaultSyncTarget ("remote").
	err = uow.Do(ctx, func(ctx context.Context) error {
		return syncRepo.EnqueueMutation(ctx, domain.SyncMutation{
			TargetKey: "remote",
			Entity:    domain.SyncEntityMemory,
			EntityKey: "mem-x",
			Op:        domain.SyncOpUpsert,
			Payload:   []byte(`{"sync_id":"mem-x"}`),
			Source:    "local",
			Project:   "proj",
		})
	})
	if err != nil {
		t.Fatalf("EnqueueMutation: %v", err)
	}

	client := &tokenCapturingClient{}
	// Token del hub DISTINTO de la clave de cola: el caso que rompía antes del fix.
	svc := service.NewSyncService(syncRepo, client, memoryRepo, uow, "cadena1234")

	res, err := svc.Push(ctx)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res.Count != 1 {
		t.Fatalf("Push debe drenar la cola pese al token distinto: se esperaba 1 fila, fue %d", res.Count)
	}
	if client.pushedToken != "cadena1234" {
		t.Errorf("la fila debe viajar bajo el token del hub: se esperaba %q, fue %q", "cadena1234", client.pushedToken)
	}
	if client.pushedRows != 1 {
		t.Errorf("el hub debe recibir 1 fila, recibió %d", client.pushedRows)
	}

	// La mutación quedó ack-eada: reejecutar no reenvía nada.
	pending, err := syncRepo.ListPending(ctx, "remote", 0, 100)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("la mutación enviada debe quedar ack-eada; quedan %d pendientes", len(pending))
	}
}
