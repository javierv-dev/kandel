package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// queueSync es un SyncRepo en memoria centrado en la cola y el registro de chunks.
type queueSync struct {
	pending []domain.SyncMutation
	acked   map[int64]bool
	chunks  map[string]bool
}

func newQueueSync(pending ...domain.SyncMutation) *queueSync {
	return &queueSync{pending: pending, acked: map[int64]bool{}, chunks: map[string]bool{}}
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
func (q *queueSync) RecordChunk(_ context.Context, _, chunkID string) error {
	q.chunks[chunkID] = true
	return nil
}
func (q *queueSync) SyncedChunks(_ context.Context, _ string) (map[string]bool, error) {
	out := map[string]bool{}
	for k, v := range q.chunks {
		out[k] = v
	}
	return out, nil
}
func (q *queueSync) AcquireLease(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}
func (q *queueSync) ReleaseLease(context.Context, string, string) error { return nil }

// memTransport es un ports.Transport en memoria.
type memTransport struct {
	manifest *ports.Manifest
	chunks   map[string][]byte
}

func newMemTransport() *memTransport {
	return &memTransport{manifest: &ports.Manifest{Version: 1}, chunks: map[string][]byte{}}
}

func (t *memTransport) ReadManifest() (*ports.Manifest, error) {
	cp := *t.manifest
	cp.Chunks = append([]ports.ChunkEntry(nil), t.manifest.Chunks...)
	return &cp, nil
}
func (t *memTransport) WriteManifest(m *ports.Manifest) error {
	cp := *m
	cp.Chunks = append([]ports.ChunkEntry(nil), m.Chunks...)
	t.manifest = &cp
	return nil
}
func (t *memTransport) WriteChunk(chunkID string, data []byte, _ ports.ChunkEntry) error {
	t.chunks[chunkID] = append([]byte(nil), data...)
	return nil
}
func (t *memTransport) ReadChunk(chunkID string) ([]byte, error) {
	return t.chunks[chunkID], nil
}

func memoryMutation(seq int64, syncID, title string) domain.SyncMutation {
	payload, _ := json.Marshal(domain.Memory{
		SyncID: syncID, Type: domain.TypeDecision, Scope: domain.ScopeProject,
		Title: title, Content: "contenido de " + title,
	})
	return domain.SyncMutation{
		Seq: seq, TargetKey: "remote", Entity: domain.SyncEntityMemory,
		EntityKey: syncID, Op: domain.SyncOpUpsert, Payload: payload,
		OccurredAt: time.Unix(1700000000+seq, 0).UTC(),
	}
}

func newSyncService(q *queueSync, tr ports.Transport, obs *fakeObs) *SyncService {
	return NewSyncService(q, tr, obs, passthroughUoW{}, fixedClock{now: time.Unix(1700000000, 0).UTC()}, "remote")
}

func TestExportWritesChunkAndAcks(t *testing.T) {
	q := newQueueSync(memoryMutation(1, "mem-a", "A"), memoryMutation(2, "mem-b", "B"))
	tr := newMemTransport()
	svc := newSyncService(q, tr, newFakeObs())

	res, err := svc.Export(context.Background())
	if err != nil {
		t.Fatalf("Export error: %v", err)
	}
	if res.Count != 2 || res.ChunkID == "" {
		t.Fatalf("export inesperado: %+v", res)
	}
	if len(tr.manifest.Chunks) != 1 {
		t.Errorf("el manifiesto debe tener un chunk: %+v", tr.manifest.Chunks)
	}
	if !q.acked[1] || !q.acked[2] {
		t.Error("las mutaciones exportadas deben quedar ack-eadas")
	}
	// Reexportar no produce otro chunk (ya están ack-eadas).
	res2, _ := svc.Export(context.Background())
	if res2.Count != 0 {
		t.Errorf("reexportar no debe drenar nada: %+v", res2)
	}
}

func TestExportEmptyDoesNothing(t *testing.T) {
	svc := newSyncService(newQueueSync(), newMemTransport(), newFakeObs())
	res, err := svc.Export(context.Background())
	if err != nil {
		t.Fatalf("Export error: %v", err)
	}
	if res.Count != 0 || res.ChunkID != "" {
		t.Errorf("sin pendientes no debe exportar: %+v", res)
	}
}

func TestImportAppliesAndIsIdempotent(t *testing.T) {
	// Lado emisor: exporta dos memorias a un transporte compartido.
	sender := newQueueSync(memoryMutation(1, "mem-a", "A"), memoryMutation(2, "mem-b", "B"))
	tr := newMemTransport()
	if _, err := newSyncService(sender, tr, newFakeObs()).Export(context.Background()); err != nil {
		t.Fatalf("Export del emisor: %v", err)
	}

	// Lado receptor: importa desde el mismo transporte a su propio almacén.
	receiverObs := newFakeObs()
	receiver := newSyncService(newQueueSync(), tr, receiverObs)
	res, err := receiver.Import(context.Background())
	if err != nil {
		t.Fatalf("Import error: %v", err)
	}
	if res.ChunksImported != 1 || res.MutationsApplied != 2 {
		t.Fatalf("import inesperado: %+v", res)
	}
	if len(receiverObs.items) != 2 {
		t.Errorf("deben haberse creado 2 memorias, hay %d", len(receiverObs.items))
	}

	// Reimportar el mismo manifiesto no aplica nada (chunk ya registrado).
	res2, err := receiver.Import(context.Background())
	if err != nil {
		t.Fatalf("reimport error: %v", err)
	}
	if res2.ChunksImported != 0 || res2.MutationsApplied != 0 {
		t.Errorf("reimportar debe ser no-op: %+v", res2)
	}
	if len(receiverObs.items) != 2 {
		t.Errorf("reimportar no debe duplicar: %d", len(receiverObs.items))
	}
}

func TestImportUpsertsBySyncID(t *testing.T) {
	receiverObs := newFakeObs()
	// Ya existe localmente una memoria con el mismo sync_id.
	receiverObs.Add(context.Background(), domain.Memory{SyncID: "mem-a", Type: domain.TypeDecision, Scope: domain.ScopeProject, Title: "viejo", Content: "viejo"})

	sender := newQueueSync(memoryMutation(1, "mem-a", "nuevo"))
	tr := newMemTransport()
	if _, err := newSyncService(sender, tr, newFakeObs()).Export(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	receiver := newSyncService(newQueueSync(), tr, receiverObs)
	if _, err := receiver.Import(context.Background()); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(receiverObs.items) != 1 {
		t.Errorf("el upsert por sync_id no debe crear una segunda fila: %d", len(receiverObs.items))
	}
	got, _ := receiverObs.GetBySyncID(context.Background(), "mem-a")
	if got == nil || got.Title != "nuevo" {
		t.Errorf("el contenido entrante debe reemplazar al existente: %+v", got)
	}
}

func TestImportVerifiesChunkID(t *testing.T) {
	tr := newMemTransport()
	// Manifiesto que apunta a un chunk cuyo contenido no casa con su id.
	tr.manifest.Chunks = []ports.ChunkEntry{{ChunkID: "idfalso", Count: 1}}
	tr.chunks["idfalso"] = []byte(`{"version":1,"mutations":[]}`)
	svc := newSyncService(newQueueSync(), tr, newFakeObs())
	if _, err := svc.Import(context.Background()); err == nil {
		t.Fatal("un chunk con id que no verifica debe fallar")
	}
}

var _ ports.SyncRepo = (*queueSync)(nil)
var _ ports.Transport = (*memTransport)(nil)
