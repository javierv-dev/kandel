package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// queueSync es un SyncRepo en memoria centrado en la cola, el cursor y la bandeja de conflictos.
type queueSync struct {
	pending   []domain.SyncMutation
	acked     map[int64]bool
	cursors   map[string]int64
	conflicts map[string]domain.SyncConflict
}

func newQueueSync(pending ...domain.SyncMutation) *queueSync {
	return &queueSync{
		pending:   pending,
		acked:     map[int64]bool{},
		cursors:   map[string]int64{},
		conflicts: map[string]domain.SyncConflict{},
	}
}

func (q *queueSync) EnqueueMutation(context.Context, domain.SyncMutation) error { return nil }
func (q *queueSync) ListPending(_ context.Context, _ string, afterSeq int64, _ int) ([]domain.SyncMutation, error) {
	var out []domain.SyncMutation
	for _, m := range q.pending {
		if !q.acked[m.Seq] && m.Seq > afterSeq {
			out = append(out, m)
		}
	}
	return out, nil
}
func (q *queueSync) Ack(_ context.Context, _ string, seqs []int64) error {
	for _, s := range seqs {
		q.acked[s] = true
	}
	return nil
}
func (q *queueSync) GetCursor(_ context.Context, target string) (int64, error) {
	return q.cursors[target], nil
}
func (q *queueSync) SetCursor(_ context.Context, target string, cursor int64) error {
	q.cursors[target] = cursor
	return nil
}
func (q *queueSync) RecordConflict(_ context.Context, c domain.SyncConflict) error {
	q.conflicts[c.SyncID] = c
	return nil
}
func (q *queueSync) ListConflicts(_ context.Context, project string) ([]domain.SyncConflict, error) {
	var out []domain.SyncConflict
	for _, c := range q.conflicts {
		if project == "" || c.Project == project {
			out = append(out, c)
		}
	}
	return out, nil
}
func (q *queueSync) GetConflict(_ context.Context, syncID string) (*domain.SyncConflict, error) {
	c, ok := q.conflicts[syncID]
	if !ok {
		return nil, nil
	}
	return &c, nil
}
func (q *queueSync) DeleteConflict(_ context.Context, syncID string) error {
	delete(q.conflicts, syncID)
	return nil
}
func (q *queueSync) CountConflicts(_ context.Context, project string) (int, error) {
	n := 0
	for _, c := range q.conflicts {
		if project == "" || c.Project == project {
			n++
		}
	}
	return n, nil
}
func (q *queueSync) AcquireLease(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}
func (q *queueSync) ReleaseLease(context.Context, string, string) error { return nil }

// fakeClient es un ports.SyncClient en memoria: registra lo que se le manda y sirve lo que se le
// programa para el Pull.
type fakeClient struct {
	pushed      [][]ports.Change // un elemento por llamada a Push
	pullChanges []ports.Change
	pullCursor  int64
	gotCursor   int64 // cursor recibido en el último Pull
}

func (c *fakeClient) Push(_ context.Context, _, _ string, changes []ports.Change) error {
	c.pushed = append(c.pushed, changes)
	return nil
}
func (c *fakeClient) Pull(_ context.Context, _, _ string, cursor int64) ([]ports.Change, int64, error) {
	c.gotCursor = cursor
	return c.pullChanges, c.pullCursor, nil
}

func memMutation(seq int64, syncID string) domain.SyncMutation {
	return domain.SyncMutation{
		Seq: seq, TargetKey: "remote", Entity: domain.SyncEntityMemory,
		EntityKey: syncID, Op: domain.SyncOpUpsert,
		OccurredAt: time.Unix(1700000000+seq, 0).UTC(),
	}
}

func changeFor(mem domain.Memory) ports.Change {
	payload, _ := json.Marshal(mem)
	return ports.Change{
		Key: mem.SyncID, Op: domain.SyncOpUpsert, Payload: payload,
		UpdatedAt: mem.UpdatedAt, Project: mem.Project,
	}
}

func newSyncService(q *queueSync, client ports.SyncClient, obs *fakeObs) *SyncService {
	return NewSyncService(q, client, obs, passthroughUoW{}, "remote")
}

func TestPushSendsCurrentStateAndAcks(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	at := time.Unix(1700000100, 0).UTC()
	obs.Add(ctx, domain.Memory{SyncID: "mem-a", Type: domain.TypeDecision, Scope: domain.ScopeProject, Title: "A", Content: "ca", Project: "proj", UpdatedAt: at})
	obs.Add(ctx, domain.Memory{SyncID: "mem-b", Type: domain.TypeDecision, Scope: domain.ScopeProject, Title: "B", Content: "cb", Project: "proj", UpdatedAt: at})

	q := newQueueSync(memMutation(1, "mem-a"), memMutation(2, "mem-b"))
	client := &fakeClient{}
	svc := newSyncService(q, client, obs)

	res, err := svc.Push(ctx)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res.Count != 2 {
		t.Fatalf("se esperaban 2 filas enviadas, fue %d", res.Count)
	}
	if len(client.pushed) != 1 || len(client.pushed[0]) != 2 {
		t.Fatalf("el hub debe recibir un lote de 2 filas: %+v", client.pushed)
	}
	if client.pushed[0][0].Op != domain.SyncOpUpsert || client.pushed[0][0].Key != "mem-a" {
		t.Errorf("fila 0 inesperada: %+v", client.pushed[0][0])
	}
	if !q.acked[1] || !q.acked[2] {
		t.Error("las mutaciones enviadas deben quedar ack-eadas")
	}
	// Reejecutar no manda nada (la cola quedó drenada).
	res2, _ := svc.Push(ctx)
	if res2.Count != 0 {
		t.Errorf("reejecutar Push no debe reenviar: %+v", res2)
	}
}

func TestPushCollapsesToCurrentStatePerSyncID(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	at := time.Unix(1700000100, 0).UTC()
	obs.Add(ctx, domain.Memory{SyncID: "mem-a", Type: domain.TypeDecision, Scope: domain.ScopeProject, Title: "final", Content: "c", UpdatedAt: at})

	// Dos mutaciones para el mismo sync_id → una sola fila (estado actual).
	q := newQueueSync(memMutation(1, "mem-a"), memMutation(2, "mem-a"))
	client := &fakeClient{}
	res, err := newSyncService(q, client, obs).Push(ctx)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res.Count != 1 || len(client.pushed[0]) != 1 {
		t.Fatalf("varias mutaciones del mismo sync_id deben colapsar a una fila: %+v", client.pushed)
	}
	if client.pushed[0][0].Payload == nil {
		t.Error("la fila debe llevar el estado actual como payload")
	}
	// Ambos seqs se drenan aunque colapsen a una fila.
	if !q.acked[1] || !q.acked[2] {
		t.Error("ambas mutaciones deben quedar ack-eadas")
	}
}

func TestPushTombstoneForDeleted(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs() // sin la memoria viva → borrada
	q := newQueueSync(memMutation(1, "mem-x"))
	client := &fakeClient{}

	res, err := newSyncService(q, client, obs).Push(ctx)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res.Count != 1 {
		t.Fatalf("un borrado debe mandar un tombstone: %+v", res)
	}
	if client.pushed[0][0].Op != domain.SyncOpDelete {
		t.Errorf("la fila debe ser un delete: %+v", client.pushed[0][0])
	}
}

func TestPushEmptyDoesNothing(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{}
	res, err := newSyncService(newQueueSync(), client, newFakeObs()).Push(ctx)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res.Count != 0 || len(client.pushed) != 0 {
		t.Errorf("sin pendientes no debe llamar al hub: %+v", res)
	}
}

func TestPullAppliesNewMemoryAndSetsCursor(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	q := newQueueSync()
	q.SetCursor(ctx, "remote", 5)

	incoming := domain.Memory{SyncID: "mem-a", Type: domain.TypeDecision, Scope: domain.ScopeProject, Title: "nuevo", Content: "c", UpdatedAt: time.Unix(1700000200, 0).UTC()}
	client := &fakeClient{pullChanges: []ports.Change{changeFor(incoming)}, pullCursor: 9}

	res, err := newSyncService(q, client, obs).Pull(ctx)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.Applied != 1 || res.NewCursor != 9 {
		t.Fatalf("pull inesperado: %+v", res)
	}
	if client.gotCursor != 5 {
		t.Errorf("Pull debe pedir desde el cursor guardado (5), pidió %d", client.gotCursor)
	}
	if got, _ := q.GetCursor(ctx, "remote"); got != 9 {
		t.Errorf("el cursor debe avanzar a 9, fue %d", got)
	}
	if m, _ := obs.GetBySyncID(ctx, "mem-a"); m == nil || m.Title != "nuevo" {
		t.Errorf("la memoria entrante debe crearse: %+v", m)
	}
}

func TestPullLWWGuardSkipsOlder(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	local := time.Unix(1700000300, 0).UTC()
	obs.Add(ctx, domain.Memory{SyncID: "mem-a", Type: domain.TypeDecision, Scope: domain.ScopeProject, Title: "local", Content: "c", UpdatedAt: local})

	// Entrante MÁS VIEJO que lo local → no debe pisar.
	older := domain.Memory{SyncID: "mem-a", Type: domain.TypeDecision, Scope: domain.ScopeProject, Title: "viejo", Content: "c", UpdatedAt: local.Add(-time.Hour)}
	client := &fakeClient{pullChanges: []ports.Change{changeFor(older)}, pullCursor: 1}
	res, err := newSyncService(newQueueSync(), client, obs).Pull(ctx)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.Applied != 0 {
		t.Errorf("un entrante más viejo no debe aplicarse: %+v", res)
	}
	if m, _ := obs.GetBySyncID(ctx, "mem-a"); m.Title != "local" {
		t.Errorf("lo local no debe pisarse: %q", m.Title)
	}

	// Entrante MÁS NUEVO → sí pisa.
	newer := domain.Memory{SyncID: "mem-a", Type: domain.TypeDecision, Scope: domain.ScopeProject, Title: "nuevo", Content: "c", UpdatedAt: local.Add(time.Hour)}
	client2 := &fakeClient{pullChanges: []ports.Change{changeFor(newer)}, pullCursor: 2}
	res2, _ := newSyncService(newQueueSync(), client2, obs).Pull(ctx)
	if res2.Applied != 1 {
		t.Errorf("un entrante más nuevo debe aplicarse: %+v", res2)
	}
	if m, _ := obs.GetBySyncID(ctx, "mem-a"); m.Title != "nuevo" {
		t.Errorf("lo nuevo debe pisar lo local: %q", m.Title)
	}
}

func TestPullForcesRecheckOnNormative(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	// El origen ya barrió la normativa (conflict_checked=true en el payload).
	incoming := domain.Memory{SyncID: "mem-a", Type: domain.TypeDecision, Scope: domain.ScopeProject, Title: "norma", Content: "c", ConflictChecked: true, UpdatedAt: time.Unix(1700000400, 0).UTC()}
	client := &fakeClient{pullChanges: []ports.Change{changeFor(incoming)}, pullCursor: 1}

	if _, err := newSyncService(newQueueSync(), client, obs).Pull(ctx); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	m, _ := obs.GetBySyncID(ctx, "mem-a")
	if m == nil || m.ConflictChecked {
		t.Errorf("una normativa importada debe quedar conflict_checked=false para re-evaluarse: %+v", m)
	}
}

func TestPullDeleteTombstone(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	local := time.Unix(1700000500, 0).UTC()
	obs.Add(ctx, domain.Memory{SyncID: "mem-a", Type: domain.TypeDecision, Scope: domain.ScopeProject, Title: "viva", Content: "c", UpdatedAt: local})

	// Tombstone más nuevo → borra localmente.
	client := &fakeClient{pullChanges: []ports.Change{{Key: "mem-a", Op: domain.SyncOpDelete, Payload: []byte("{}"), UpdatedAt: local.Add(time.Hour)}}, pullCursor: 1}
	res, err := newSyncService(newQueueSync(), client, obs).Pull(ctx)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.Applied != 1 {
		t.Errorf("el tombstone más nuevo debe aplicarse: %+v", res)
	}
	if m, _ := obs.GetBySyncID(ctx, "mem-a"); m != nil {
		t.Errorf("la memoria debe quedar borrada: %+v", m)
	}
}

func TestPullDetectsConcurrentEditAndDoesNotOverwrite(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	local := time.Unix(1700000600, 0).UTC()
	obs.Add(ctx, domain.Memory{SyncID: "j1", Type: domain.TypeJournal, Scope: domain.ScopeProject, Title: "abierta", Content: "editando", Project: "proj", Resolution: domain.ResolutionOpen, UpdatedAt: local})

	// Hay una mutación local PENDIENTE para j1 (la estoy editando y aún no subo).
	q := newQueueSync(memMutation(1, "j1"))
	// Llega la versión remota (otra máquina la cerró), más nueva.
	remote := domain.Memory{SyncID: "j1", Type: domain.TypeJournal, Scope: domain.ScopeProject, Title: "cerrada", Content: "resuelta", Project: "proj", Resolution: domain.ResolutionResolved, UpdatedAt: local.Add(time.Hour)}
	client := &fakeClient{pullChanges: []ports.Change{changeFor(remote)}, pullCursor: 3}

	res, err := newSyncService(q, client, obs).Pull(ctx)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if res.Conflicts != 1 || res.Applied != 0 {
		t.Fatalf("debe detectar 1 conflicto y no aplicar: %+v", res)
	}
	// NO se pisó lo local: la memoria sigue como la dejé.
	if m, _ := obs.GetBySyncID(ctx, "j1"); m == nil || m.Title != "abierta" {
		t.Errorf("no debe pisarse la edición local: %+v", m)
	}
	// La remota quedó en la bandeja de conflictos.
	if len(q.conflicts) != 1 {
		t.Errorf("la fila remota debe quedar en la bandeja de conflictos: %+v", q.conflicts)
	}
	// El cursor avanza igual (la fila no se pierde, está en la bandeja).
	if got, _ := q.GetCursor(ctx, "remote"); got != 3 {
		t.Errorf("el cursor debe avanzar a 3, fue %d", got)
	}
}

func TestResolveConflictTakeRemote(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	local := time.Unix(1700000700, 0).UTC()
	obs.Add(ctx, domain.Memory{SyncID: "j1", Type: domain.TypeJournal, Scope: domain.ScopeProject, Title: "local", Content: "c", UpdatedAt: local})

	q := newQueueSync()
	remote := domain.Memory{SyncID: "j1", Type: domain.TypeJournal, Scope: domain.ScopeProject, Title: "remota", Content: "c", UpdatedAt: local.Add(time.Hour)}
	q.RecordConflict(ctx, domain.SyncConflict{SyncID: "j1", IncomingOp: domain.SyncOpUpsert, Incoming: mustJSON(remote), IncomingAt: remote.UpdatedAt})

	svc := newSyncService(q, &fakeClient{}, obs)
	if err := svc.ResolveConflict(ctx, "j1", true); err != nil {
		t.Fatalf("ResolveConflict: %v", err)
	}
	if m, _ := obs.GetBySyncID(ctx, "j1"); m == nil || m.Title != "remota" {
		t.Errorf("tomar la remota debe aplicar su contenido: %+v", m)
	}
	if len(q.conflicts) != 0 {
		t.Error("el conflicto debe quedar resuelto (borrado de la bandeja)")
	}
}

func TestResolveConflictKeepLocal(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	local := time.Unix(1700000800, 0).UTC()
	obs.Add(ctx, domain.Memory{SyncID: "j1", Type: domain.TypeJournal, Scope: domain.ScopeProject, Title: "local", Content: "c", UpdatedAt: local})

	q := newQueueSync()
	remote := domain.Memory{SyncID: "j1", Type: domain.TypeJournal, Scope: domain.ScopeProject, Title: "remota", Content: "c", UpdatedAt: local.Add(time.Hour)}
	q.RecordConflict(ctx, domain.SyncConflict{SyncID: "j1", IncomingOp: domain.SyncOpUpsert, Incoming: mustJSON(remote), IncomingAt: remote.UpdatedAt})

	svc := newSyncService(q, &fakeClient{}, obs)
	if err := svc.ResolveConflict(ctx, "j1", false); err != nil {
		t.Fatalf("ResolveConflict: %v", err)
	}
	// Conservar la local: NO se pisa; la memoria sigue como estaba.
	if m, _ := obs.GetBySyncID(ctx, "j1"); m == nil || m.Title != "local" {
		t.Errorf("conservar la local no debe pisar: %+v", m)
	}
	if len(q.conflicts) != 0 {
		t.Error("el conflicto debe quedar resuelto igualmente")
	}
}

func mustJSON(m domain.Memory) []byte {
	b, _ := json.Marshal(m)
	return b
}

var (
	_ ports.SyncRepo           = (*queueSync)(nil)
	_ ports.SyncClient         = (*fakeClient)(nil)
	_ ports.SyncConflictReader = (*queueSync)(nil)
)
