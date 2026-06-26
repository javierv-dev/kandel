package git

import (
	"testing"

	"github.com/javierv-dev/kandel/internal/ports"
)

func TestManifestRoundTrip(t *testing.T) {
	tr := New(t.TempDir())

	// Sin archivo aún → manifiesto vacío versión 1.
	m, err := tr.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest vacío: %v", err)
	}
	if m.Version != 1 || len(m.Chunks) != 0 {
		t.Errorf("manifiesto inicial inesperado: %+v", m)
	}

	m.Chunks = append(m.Chunks, ports.ChunkEntry{ChunkID: "abc", Count: 3, Size: 99})
	if err := tr.WriteManifest(m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	got, err := tr.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if len(got.Chunks) != 1 || got.Chunks[0].ChunkID != "abc" || got.Chunks[0].Count != 3 {
		t.Errorf("manifiesto mal persistido: %+v", got)
	}
}

func TestChunkRoundTrip(t *testing.T) {
	tr := New(t.TempDir())
	payload := []byte(`{"hola":"mundo"}`)
	if err := tr.WriteChunk("deadbeef", payload, ports.ChunkEntry{ChunkID: "deadbeef", Count: 1}); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	got, err := tr.ReadChunk("deadbeef")
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("chunk mal leído: %q", got)
	}
}

func TestReadMissingChunkFails(t *testing.T) {
	tr := New(t.TempDir())
	if _, err := tr.ReadChunk("noexiste"); err == nil {
		t.Error("leer un chunk inexistente debe fallar")
	}
}
