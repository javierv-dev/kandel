package hub_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/adapter/httpsync"
	"github.com/javierv-dev/kandel/internal/adapter/sqlite"
	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/hub"
	"github.com/javierv-dev/kandel/internal/ports"
)

// newHubServer levanta el hub con un store SQLite real y un cliente httpsync apuntando a él. Prueba el
// contrato de red completo cliente↔hub (misma serialización, mismas rutas), no solo cada mitad.
func newHubServer(t *testing.T) (*httpsync.Client, func()) {
	t.Helper()
	store, err := sqlite.OpenHubStore(context.Background(), filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("OpenHubStore: %v", err)
	}
	srv := httptest.NewServer(hub.Handler(store))
	cleanup := func() {
		srv.Close()
		store.Close()
	}
	return httpsync.NewClient(srv.URL), cleanup
}

func change(key string, at time.Time, payload string) ports.Change {
	return ports.Change{Key: key, Op: domain.SyncOpUpsert, Payload: []byte(payload), UpdatedAt: at}
}

func TestEndToEndPushPullCursor(t *testing.T) {
	cl, cleanup := newHubServer(t)
	defer cleanup()
	ctx := context.Background()
	base := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)

	if err := cl.Push(ctx, "memory", "tok", []ports.Change{change("a", base, `{"n":1}`), change("b", base, `{"n":2}`)}); err != nil {
		t.Fatalf("Push: %v", err)
	}
	changes, cursor, err := cl.Pull(ctx, "memory", "tok", 0)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(changes) != 2 || cursor != 2 {
		t.Fatalf("se esperaban 2 cambios cursor 2, vino %d cursor %d", len(changes), cursor)
	}
	// El payload sobrevive el viaje de ida y vuelta intacto.
	if string(changes[0].Payload) != `{"n":1}` {
		t.Errorf("payload alterado en el viaje: %s", changes[0].Payload)
	}

	// Pull incremental desde el cursor: nada nuevo.
	changes, cursor, err = cl.Pull(ctx, "memory", "tok", cursor)
	if err != nil {
		t.Fatalf("Pull incremental: %v", err)
	}
	if len(changes) != 0 || cursor != 2 {
		t.Fatalf("pull incremental debe venir vacío cursor 2, vino %d cursor %d", len(changes), cursor)
	}

	// Un cambio más, otra máquina lo baja incremental.
	if err := cl.Push(ctx, "memory", "tok", []ports.Change{change("c", base, `{"n":3}`)}); err != nil {
		t.Fatalf("Push c: %v", err)
	}
	changes, cursor, err = cl.Pull(ctx, "memory", "tok", 2)
	if err != nil {
		t.Fatalf("Pull c: %v", err)
	}
	if len(changes) != 1 || changes[0].Key != "c" || cursor != 3 {
		t.Fatalf("pull desde 2 debe traer solo c cursor 3, vino %d cursor %d", len(changes), cursor)
	}
}

func TestEndToEndLWWConcurrente(t *testing.T) {
	cl, cleanup := newHubServer(t)
	defer cleanup()
	ctx := context.Background()
	t1 := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)

	// Máquina A escribe la versión más nueva; máquina B, la más vieja, después.
	if err := cl.Push(ctx, "memory", "tok", []ports.Change{change("k", t2, `"A-nuevo"`)}); err != nil {
		t.Fatalf("Push A: %v", err)
	}
	if err := cl.Push(ctx, "memory", "tok", []ports.Change{change("k", t1, `"B-viejo"`)}); err != nil {
		t.Fatalf("Push B: %v", err)
	}

	// Una tercera máquina baja el estado: gana la edición con updated_at mayor.
	changes, _, err := cl.Pull(ctx, "memory", "tok", 0)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(changes) != 1 || string(changes[0].Payload) != `"A-nuevo"` {
		t.Fatalf("debe ganar la edición más reciente (updated_at mayor): %+v", changes)
	}
	if !changes[0].UpdatedAt.Equal(t2) {
		t.Errorf("updated_at servido inesperado: %v", changes[0].UpdatedAt)
	}
}

func TestColdStartTraeEstadoActual(t *testing.T) {
	cl, cleanup := newHubServer(t)
	defer cleanup()
	ctx := context.Background()
	base := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)

	if err := cl.Push(ctx, "memory", "tok", []ports.Change{change("a", base, `"x"`), change("b", base, `"y"`)}); err != nil {
		t.Fatalf("Push: %v", err)
	}
	// Máquina nueva con cursor=0 → estado actual completo (una fila por key viva).
	changes, cursor, err := cl.Pull(ctx, "memory", "tok", 0)
	if err != nil {
		t.Fatalf("Pull frío: %v", err)
	}
	if len(changes) != 2 || cursor != 2 {
		t.Fatalf("arranque en frío debe traer el estado actual completo: %d cambios cursor %d", len(changes), cursor)
	}
}

func TestAuthRechazaTokenIncorrecto(t *testing.T) {
	store, err := sqlite.OpenHubStore(context.Background(), filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("OpenHubStore: %v", err)
	}
	defer store.Close()
	srv := httptest.NewServer(hub.Handler(store))
	defer srv.Close()

	// Bearer que NO coincide con el token de la ruta → 401.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/memory/tok/changes?since=0", nil)
	req.Header.Set("Authorization", "Bearer otro")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("un Bearer que no coincide debe dar 401, dio %d", resp.StatusCode)
	}

	// Sin header Authorization → 401.
	resp2, err := http.Get(srv.URL + "/memory/tok/changes?since=0")
	if err != nil {
		t.Fatalf("request sin auth: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("sin Authorization debe dar 401, dio %d", resp2.StatusCode)
	}
}

func TestCuerpoInvalidoDa400(t *testing.T) {
	store, err := sqlite.OpenHubStore(context.Background(), filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("OpenHubStore: %v", err)
	}
	defer store.Close()
	srv := httptest.NewServer(hub.Handler(store))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/memory/tok/changes", strings.NewReader("no-json"))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("un cuerpo no-JSON debe dar 400, dio %d", resp.StatusCode)
	}
}
