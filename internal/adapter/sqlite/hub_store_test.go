package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/hub"
)

func newTestHubStore(t *testing.T) *HubStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hub.db")
	s, err := OpenHubStore(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenHubStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func hubChange(key string, at time.Time) hub.Change {
	return hub.Change{Key: key, Op: "upsert", Payload: []byte(`{"k":"` + key + `"}`), UpdatedAt: at}
}

func TestHubStoreApplyAndSince(t *testing.T) {
	s := newTestHubStore(t)
	ctx := context.Background()
	base := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)

	// Dos filas nuevas → server_seq 1 y 2; pull desde 0 las trae ambas.
	if err := s.Apply(ctx, "memory", "tok", []hub.Change{hubChange("a", base), hubChange("b", base)}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	changes, cursor, err := s.Since(ctx, "memory", "tok", 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(changes) != 2 || cursor != 2 {
		t.Fatalf("se esperaban 2 cambios y cursor 2, se obtuvo %d cambios cursor %d", len(changes), cursor)
	}

	// Pull incremental: desde el cursor 2 no hay nada nuevo.
	changes, cursor, err = s.Since(ctx, "memory", "tok", 2)
	if err != nil {
		t.Fatalf("Since incremental: %v", err)
	}
	if len(changes) != 0 || cursor != 2 {
		t.Fatalf("pull incremental debe venir vacío con cursor 2, vino %d cambios cursor %d", len(changes), cursor)
	}

	// Una fila nueva → server_seq 3; pull desde 2 la trae sola.
	if err := s.Apply(ctx, "memory", "tok", []hub.Change{hubChange("c", base)}); err != nil {
		t.Fatalf("Apply c: %v", err)
	}
	changes, cursor, err = s.Since(ctx, "memory", "tok", 2)
	if err != nil {
		t.Fatalf("Since c: %v", err)
	}
	if len(changes) != 1 || changes[0].Key != "c" || cursor != 3 {
		t.Fatalf("pull desde 2 debe traer solo c con cursor 3, vino %d cambios cursor %d", len(changes), cursor)
	}
}

func TestHubStoreLWWNewerWins(t *testing.T) {
	s := newTestHubStore(t)
	ctx := context.Background()
	t1 := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)

	// Estado inicial de la key con t1 (server_seq 1).
	if err := s.Apply(ctx, "memory", "tok", []hub.Change{{Key: "k", Op: "upsert", Payload: []byte(`"v1"`), UpdatedAt: t1}}); err != nil {
		t.Fatalf("Apply t1: %v", err)
	}

	// Una edición MÁS VIEJA (t1-1h) NO debe pisar ni bumpear server_seq.
	older := t1.Add(-time.Hour)
	if err := s.Apply(ctx, "memory", "tok", []hub.Change{{Key: "k", Op: "upsert", Payload: []byte(`"viejo"`), UpdatedAt: older}}); err != nil {
		t.Fatalf("Apply older: %v", err)
	}
	changes, cursor, _ := s.Since(ctx, "memory", "tok", 0)
	if len(changes) != 1 || string(changes[0].Payload) != `"v1"` || cursor != 1 {
		t.Fatalf("la edición vieja no debe ganar: %d cambios payload %s cursor %d", len(changes), payloadOf(changes), cursor)
	}

	// Una edición MÁS NUEVA (t2) sí gana y bumpea server_seq a 2.
	if err := s.Apply(ctx, "memory", "tok", []hub.Change{{Key: "k", Op: "upsert", Payload: []byte(`"v2"`), UpdatedAt: t2}}); err != nil {
		t.Fatalf("Apply t2: %v", err)
	}
	changes, cursor, _ = s.Since(ctx, "memory", "tok", 0)
	if len(changes) != 1 || string(changes[0].Payload) != `"v2"` || cursor != 2 {
		t.Fatalf("la edición nueva debe ganar con cursor 2: %d cambios payload %s cursor %d", len(changes), payloadOf(changes), cursor)
	}
}

func TestHubStoreEqualUpdatedAtIgnored(t *testing.T) {
	s := newTestHubStore(t)
	ctx := context.Background()
	at := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)

	if err := s.Apply(ctx, "memory", "tok", []hub.Change{{Key: "k", Op: "upsert", Payload: []byte(`"v1"`), UpdatedAt: at}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Mismo updated_at: LWW es estricto (>), así que se ignora (no reescribe ni bumpea).
	if err := s.Apply(ctx, "memory", "tok", []hub.Change{{Key: "k", Op: "upsert", Payload: []byte(`"v2"`), UpdatedAt: at}}); err != nil {
		t.Fatalf("Apply igual: %v", err)
	}
	changes, cursor, _ := s.Since(ctx, "memory", "tok", 0)
	if len(changes) != 1 || string(changes[0].Payload) != `"v1"` || cursor != 1 {
		t.Fatalf("updated_at igual debe ignorarse: %d cambios payload %s cursor %d", len(changes), payloadOf(changes), cursor)
	}
}

func TestHubStoreTokensAislados(t *testing.T) {
	s := newTestHubStore(t)
	ctx := context.Background()
	at := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)

	if err := s.Apply(ctx, "memory", "tokA", []hub.Change{hubChange("a", at)}); err != nil {
		t.Fatalf("Apply A: %v", err)
	}
	// Otro token no ve las filas del primero (slots aislados).
	changes, cursor, _ := s.Since(ctx, "memory", "tokB", 0)
	if len(changes) != 0 || cursor != 0 {
		t.Fatalf("tokB no debe ver filas de tokA: %d cambios cursor %d", len(changes), cursor)
	}
	// Y su server_seq arranca independiente en 1.
	if err := s.Apply(ctx, "memory", "tokB", []hub.Change{hubChange("b", at)}); err != nil {
		t.Fatalf("Apply B: %v", err)
	}
	_, cursor, _ = s.Since(ctx, "memory", "tokB", 0)
	if cursor != 1 {
		t.Fatalf("server_seq de tokB debe arrancar en 1, fue %d", cursor)
	}
}

func TestHubStoreDeleteTombstoneViaja(t *testing.T) {
	s := newTestHubStore(t)
	ctx := context.Background()
	t1 := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)

	if err := s.Apply(ctx, "memory", "tok", []hub.Change{{Key: "k", Op: "upsert", Payload: []byte(`"v"`), UpdatedAt: t1}}); err != nil {
		t.Fatalf("Apply upsert: %v", err)
	}
	// Un delete más nuevo se guarda como tombstone y viaja en el pull.
	if err := s.Apply(ctx, "memory", "tok", []hub.Change{{Key: "k", Op: "delete", Payload: []byte(`{}`), UpdatedAt: t2}}); err != nil {
		t.Fatalf("Apply delete: %v", err)
	}
	changes, _, _ := s.Since(ctx, "memory", "tok", 0)
	if len(changes) != 1 || changes[0].Op != "delete" {
		t.Fatalf("el tombstone debe viajar: %+v", changes)
	}
}

func payloadOf(changes []hub.Change) string {
	if len(changes) == 0 {
		return "<vacío>"
	}
	return string(changes[0].Payload)
}
