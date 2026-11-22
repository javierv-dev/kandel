package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
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
func (q *queueSync) ListPending(_ context.Context, _ string, afterSeq int64, limit int) ([]domain.SyncMutation, error) {
	var out []domain.SyncMutation
	for _, m := range q.pending {
		if q.acked[m.Seq] || m.Seq <= afterSeq {
			continue
		}
		out = append(out, m)
		if limit > 0 && len(out) >= limit {
			break
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
	pushedToken string           // token/namespace recibido en el último Push
	pullChanges []ports.Change
	pullCursor  int64
	gotCursor   int64 // cursor recibido en el último Pull
}

func (c *fakeClient) Push(_ context.Context, _, token string, changes []ports.Change) error {
	c.pushedToken = token
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

// blockingClient es un ports.SyncClient cuyo Push se detiene hasta que el test lo libera. Sirve para
// mantener el guard de exclusión tomado y comprobar que un segundo auto-sync se omite.
type blockingClient struct {
	entered chan struct{}
	release chan struct{}
}

func (c *blockingClient) Push(context.Context, string, string, []ports.Change) error {
	c.entered <- struct{}{}
	<-c.release
	return nil
}
func (c *blockingClient) Pull(context.Context, string, string, int64) ([]ports.Change, int64, error) {
	return nil, 0, nil
}

func TestAutoPushSkipsWhenAnotherSyncRuns(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	at := time.Unix(1700000100, 0).UTC()
	obs.Add(ctx, domain.Memory{SyncID: "mem-a", Type: domain.TypeDecision, Scope: domain.ScopeProject, Title: "A", Content: "c", UpdatedAt: at})
	q := newQueueSync(memMutation(1, "mem-a"))
	client := &blockingClient{entered: make(chan struct{}), release: make(chan struct{})}
	svc := newSyncService(q, client, obs)

	// Primer auto-push: toma el guard y queda bloqueado dentro del client.
	firstErr := make(chan error, 1)
	go func() {
		_, skipped, err := svc.AutoPush(ctx)
		if skipped {
			err = errors.New("el primer auto-push no debía omitirse")
		}
		firstErr <- err
	}()
	<-client.entered // el primero ya tiene el guard

	// Segundo disparo mientras el primero sigue: debe omitirse (skipped), sin tocar el client.
	if _, skipped, err := svc.AutoPush(ctx); err != nil {
		t.Fatalf("segundo AutoPush: %v", err)
	} else if !skipped {
		t.Fatal("el segundo auto-sync debe omitirse mientras hay uno en curso")
	}
	// AutoPull comparte el mismo guard: también se omite.
	if _, skipped, err := svc.AutoPull(ctx); err != nil {
		t.Fatalf("AutoPull concurrente: %v", err)
	} else if !skipped {
		t.Fatal("AutoPull debe omitirse mientras el push tiene el guard")
	}

	close(client.release) // libera el primero
	if err := <-firstErr; err != nil {
		t.Fatalf("primer auto-push: %v", err)
	}
	// Tras liberar, un nuevo auto-push ya no se omite (la cola quedó drenada por el primero).
	if _, skipped, err := svc.AutoPush(ctx); err != nil || skipped {
		t.Fatalf("tras liberar el guard, AutoPush no debe omitirse: skipped=%v err=%v", skipped, err)
	}
}

func mustJSON(m domain.Memory) []byte {
	b, _ := json.Marshal(m)
	return b
}

func TestWireMemoryMatchesDomainMemoryFields(t *testing.T) {
	domType := reflect.TypeOf(domain.Memory{})
	wireType := reflect.TypeOf(wireMemory{})
	if wireType.NumField() != domType.NumField()-1 {
		t.Fatalf("wireMemory tiene %d campos y domain.Memory %d (debe cubrir todos menos UnknownFields)",
			wireType.NumField(), domType.NumField())
	}
	for i := 0; i < wireType.NumField(); i++ {
		f := wireType.Field(i)
		if _, ok := domType.FieldByName(f.Name); !ok {
			t.Errorf("campo %q de wireMemory no existe en domain.Memory", f.Name)
		}
		if !knownMemoryKeys[stringsLower(f.Name)] {
			t.Errorf("campo %q falta en knownMemoryKeys", f.Name)
		}
	}
}

func stringsLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func TestPullAndPushPreserveUnknownFieldsAcrossEdit(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	q := newQueueSync()
	t1 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	// Máquina A (versión futura) sube una memoria con campos nuevos desconocidos para esta versión.
	futurePayload := []byte(`{
		"SyncID":"mem-future",
		"Project":"kandel",
		"Type":"decision",
		"Scope":"project",
		"Title":"título de A",
		"Content":"contenido de A",
		"CreatedAt":"2026-11-20T10:00:00Z",
		"UpdatedAt":"2026-11-20T10:00:00Z",
		"Trigger":"antes de hacer commit",
		"Evidence":"measured",
		"FutureMeta":{"version":2,"flags":["x","y"]}
	}`)
	client := &fakeClient{
		pullChanges: []ports.Change{{
			Key:       "mem-future",
			Op:        domain.SyncOpUpsert,
			Payload:   futurePayload,
			UpdatedAt: t1,
			Project:   "kandel",
		}},
		pullCursor: 1,
	}

	svc := newSyncService(q, client, obs)
	pullRes, err := svc.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if pullRes.Applied != 1 || pullRes.SchemaDrift != 1 {
		t.Fatalf("Pull debía reportar Applied=1 y SchemaDrift=1, fue: %+v", pullRes)
	}

	stored, err := obs.GetBySyncID(ctx, "mem-future")
	if err != nil || stored == nil {
		t.Fatalf("GetBySyncID: %v (%v)", stored, err)
	}
	if len(stored.UnknownFields) != 3 {
		t.Fatalf("esperaba 3 campos desconocidos preservados, obtuve: %+v", stored.UnknownFields)
	}

	// Máquina B (esta versión vieja) edita la memoria localmente con un timestamp más nuevo y la reenvía.
	stored.Title = "título editado en B"
	stored.UpdatedAt = t1.Add(time.Hour)
	if _, err := obs.Update(ctx, stored.ID, *stored); err != nil {
		t.Fatalf("Update: %v", err)
	}

	qPush := newQueueSync(memMutation(1, "mem-future"))
	pushClient := &fakeClient{}
	if _, err := newSyncService(qPush, pushClient, obs).Push(ctx); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(pushClient.pushed) != 1 || len(pushClient.pushed[0]) != 1 {
		t.Fatalf("esperaba 1 cambio en Push, fue: %+v", pushClient.pushed)
	}

	var rePushed map[string]any
	if err := json.Unmarshal(pushClient.pushed[0][0].Payload, &rePushed); err != nil {
		t.Fatalf("payload reenviado no es JSON válido: %v", err)
	}
	if rePushed["Title"] != "título editado en B" {
		t.Errorf("Title debía reflejar la edición de B, fue %v", rePushed["Title"])
	}
	if rePushed["Trigger"] != "antes de hacer commit" || rePushed["Evidence"] != "measured" {
		t.Errorf("los campos futuros se perdieron tras el viaje LWW: %+v", rePushed)
	}
	if _, ok := rePushed["UnknownFields"]; ok {
		t.Errorf("no debe filtrarse una clave literal UnknownFields al JSON del hub: %+v", rePushed)
	}
	meta, ok := rePushed["FutureMeta"].(map[string]any)
	if !ok || meta["version"] != float64(2) {
		t.Errorf("FutureMeta estructurado no se preservó intacto: %v", rePushed["FutureMeta"])
	}
}

func TestResolveConflictPreservesUnknownFields(t *testing.T) {
	ctx := context.Background()
	obs := newFakeObs()
	local := time.Unix(1700000700, 0).UTC()
	obs.Add(ctx, domain.Memory{SyncID: "j1", Type: domain.TypeJournal, Scope: domain.ScopeProject, Title: "local", Content: "c", UpdatedAt: local})

	q := newQueueSync()
	remotePayload := []byte(`{"SyncID":"j1","Type":"journal","Scope":"project","Title":"remota","Content":"c","UpdatedAt":"2026-11-20T12:00:00Z","FutureBadge":"gold"}`)
	q.RecordConflict(ctx, domain.SyncConflict{SyncID: "j1", IncomingOp: domain.SyncOpUpsert, Incoming: remotePayload, IncomingAt: local.Add(time.Hour)})

	svc := newSyncService(q, &fakeClient{}, obs)
	if err := svc.ResolveConflict(ctx, "j1", true); err != nil {
		t.Fatalf("ResolveConflict: %v", err)
	}
	got, _ := obs.GetBySyncID(ctx, "j1")
	if got == nil || got.UnknownFields["FutureBadge"] != `"gold"` {
		t.Errorf("ResolveConflict(takeRemote) debía preservar FutureBadge, fue: %+v", got)
	}
}

var (
	_ ports.SyncRepo           = (*queueSync)(nil)
	_ ports.SyncClient         = (*fakeClient)(nil)
	_ ports.SyncConflictReader = (*queueSync)(nil)
)
