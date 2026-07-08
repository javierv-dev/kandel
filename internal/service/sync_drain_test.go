package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// TestPushDrainsMultipleBatches verifica que un solo Push vacía la cola aunque tenga más pendientes que
// el tope de lote (pushBatchLimit): antes procesaba un solo lote y hacía falta reejecutar.
func TestPushDrainsMultipleBatches(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	at := time.Unix(1700000100, 0).UTC()

	n := pushBatchLimit + 10
	muts := make([]domain.SyncMutation, 0, n)
	for i := 1; i <= n; i++ {
		key := fmt.Sprintf("mem-%d", i)
		obs.Add(ctx, domain.Memory{SyncID: key, Type: domain.TypeDecision, Scope: domain.ScopeProject, Title: key, Content: "c", UpdatedAt: at})
		muts = append(muts, memMutation(int64(i), key))
	}
	q := newQueueSync(muts...)
	client := &fakeClient{}

	res, err := newSyncService(q, client, obs).Push(ctx)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res.Count != n {
		t.Fatalf("Push debe drenar toda la cola (%d), envió %d", n, res.Count)
	}
	if len(client.pushed) != 2 {
		t.Fatalf("con %d pendientes y tope %d se esperaban 2 lotes, hubo %d", n, pushBatchLimit, len(client.pushed))
	}
	if len(client.pushed[0]) != pushBatchLimit || len(client.pushed[1]) != n-pushBatchLimit {
		t.Errorf("tamaños de lote inesperados: %d y %d", len(client.pushed[0]), len(client.pushed[1]))
	}
	// Toda la cola quedó ack-eada: reejecutar no reenvía nada.
	if res2, _ := newSyncService(q, client, obs).Push(ctx); res2.Count != 0 {
		t.Errorf("reejecutar tras drenar no debe reenviar: %+v", res2)
	}
}

// TestPullNoSpuriousConflictOnEcho verifica que Pull NO marca conflicto cuando la fila remota es el eco
// de nuestro propio push (misma memoria, mismo updated_at) aunque haya una mutación local pendiente para
// esa key. Antes, push+pull seguidos generaban un "conflicto" espurio.
func TestPullNoSpuriousConflictOnEcho(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	local := time.Unix(1700000600, 0).UTC()
	mem := domain.Memory{SyncID: "mem-a", Type: domain.TypeDecision, Scope: domain.ScopeProject, Title: "igual", Content: "c", Project: "proj", UpdatedAt: local}
	obs.Add(ctx, mem)

	// Mutación local pendiente para mem-a (aún sin ackear tras el push).
	q := newQueueSync(memMutation(1, "mem-a"))
	// El hub devuelve la MISMA fila que subimos (mismo updated_at): es un eco, no una edición concurrente.
	client := &fakeClient{pullChanges: []ports.Change{changeFor(mem)}, pullCursor: 3}

	res, err := newSyncService(q, client, obs).Pull(ctx)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.Conflicts != 0 {
		t.Errorf("un eco del propio push no debe marcar conflicto: %+v", res)
	}
	if res.Applied != 0 {
		t.Errorf("un eco (mismo updated_at) no debe aplicarse por LWW: %+v", res)
	}
	if len(q.conflicts) != 0 {
		t.Errorf("no debe quedar nada en la bandeja de conflictos: %+v", q.conflicts)
	}
	// El cursor avanza igual.
	if got, _ := q.GetCursor(ctx, "remote"); got != 3 {
		t.Errorf("el cursor debe avanzar a 3, fue %d", got)
	}
}
