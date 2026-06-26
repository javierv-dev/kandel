package chunkcodec

import (
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
)

func sampleMutations() []domain.SyncMutation {
	return []domain.SyncMutation{
		{Seq: 1, TargetKey: "remote", Entity: domain.SyncEntityMemory, EntityKey: "mem-1", Op: domain.SyncOpUpsert, Payload: []byte(`{"a":1}`), OccurredAt: time.Unix(1700000000, 0).UTC()},
		{Seq: 2, TargetKey: "remote", Entity: domain.SyncEntitySession, EntityKey: "s1", Op: domain.SyncOpUpsert, Payload: []byte(`{}`), OccurredAt: time.Unix(1700000001, 0).UTC()},
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	muts := sampleMutations()
	data, id, err := Encode(muts)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if id == "" {
		t.Fatal("ChunkID vacío")
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(got) != 2 || got[0].EntityKey != "mem-1" || got[1].Entity != domain.SyncEntitySession {
		t.Errorf("round-trip incorrecto: %+v", got)
	}
}

func TestEncodeIsDeterministic(t *testing.T) {
	_, id1, _ := Encode(sampleMutations())
	_, id2, _ := Encode(sampleMutations())
	if id1 != id2 {
		t.Errorf("el mismo lote debe dar el mismo ChunkID: %s != %s", id1, id2)
	}
}

func TestVerifyID(t *testing.T) {
	data, id, _ := Encode(sampleMutations())
	if err := VerifyID(data, id); err != nil {
		t.Errorf("VerifyID debe pasar con el id correcto: %v", err)
	}
	if err := VerifyID(data, "otracosa"); err == nil {
		t.Error("VerifyID debe fallar con un id incorrecto")
	}
}

func TestDecodeRejectsBadJSON(t *testing.T) {
	if _, err := Decode([]byte("no es json")); err == nil {
		t.Error("Decode debe fallar con JSON inválido")
	}
}
