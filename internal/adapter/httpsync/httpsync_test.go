package httpsync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

func TestPush(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody pushRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cl := NewClient(server.URL)
	updated := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	changes := []ports.Change{
		{Key: "abc", Op: domain.SyncOpUpsert, Payload: []byte(`{"title":"hola"}`), UpdatedAt: updated, Project: "kandel"},
		{Key: "def", Op: domain.SyncOpDelete, Payload: []byte(`{}`), UpdatedAt: updated},
	}
	if err := cl.Push(context.Background(), "memory", "tok123", changes); err != nil {
		t.Fatalf("Push: %v", err)
	}

	if gotPath != "/memory/tok123/changes" {
		t.Errorf("ruta inesperada: %s", gotPath)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("auth inesperada: %s", gotAuth)
	}
	if len(gotBody.Changes) != 2 {
		t.Fatalf("se esperaban 2 cambios, se obtuvo %d", len(gotBody.Changes))
	}
	if gotBody.Changes[0].Key != "abc" || gotBody.Changes[0].Op != "upsert" {
		t.Errorf("cambio 0 inesperado: %+v", gotBody.Changes[0])
	}
	// El payload viaja como JSON embebido, no como base64.
	if string(gotBody.Changes[0].Payload) != `{"title":"hola"}` {
		t.Errorf("payload no viajó como JSON embebido: %s", gotBody.Changes[0].Payload)
	}
	if gotBody.Changes[1].Op != "delete" {
		t.Errorf("op de borrado inesperada: %s", gotBody.Changes[1].Op)
	}
}

func TestPushEmptySkipsCall(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer server.Close()

	cl := NewClient(server.URL)
	if err := cl.Push(context.Background(), "memory", "tok", nil); err != nil {
		t.Fatalf("Push vacío: %v", err)
	}
	if called {
		t.Error("un lote vacío no debe llamar al hub")
	}
}

func TestPull(t *testing.T) {
	var gotSince, gotAuth string
	updated := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSince = r.URL.Query().Get("since")
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(pullResponse{
			Changes: []wireChange{
				{Key: "abc", Op: "upsert", Payload: json.RawMessage(`{"title":"hola"}`), UpdatedAt: updated, Project: "kandel"},
			},
			Cursor: 42,
		})
	}))
	defer server.Close()

	cl := NewClient(server.URL)
	changes, cursor, err := cl.Pull(context.Background(), "memory", "tok123", 7)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if gotSince != "7" {
		t.Errorf("since inesperado: %s", gotSince)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("auth inesperada: %s", gotAuth)
	}
	if cursor != 42 {
		t.Errorf("cursor inesperado: %d", cursor)
	}
	if len(changes) != 1 {
		t.Fatalf("se esperaba 1 cambio, se obtuvo %d", len(changes))
	}
	got := changes[0]
	if got.Key != "abc" || got.Op != domain.SyncOpUpsert || got.Project != "kandel" {
		t.Errorf("cambio inesperado: %+v", got)
	}
	if string(got.Payload) != `{"title":"hola"}` {
		t.Errorf("payload inesperado: %s", got.Payload)
	}
	if !got.UpdatedAt.Equal(updated) {
		t.Errorf("updated_at inesperado: %v", got.UpdatedAt)
	}
}

func TestPullColdStart(t *testing.T) {
	var gotSince string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSince = r.URL.Query().Get("since")
		_ = json.NewEncoder(w).Encode(pullResponse{Changes: []wireChange{}, Cursor: 0})
	}))
	defer server.Close()

	cl := NewClient(server.URL)
	changes, cursor, err := cl.Pull(context.Background(), "memory", "tok", 0)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if gotSince != "0" {
		t.Errorf("arranque en frío debe pedir since=0, pidió %s", gotSince)
	}
	if len(changes) != 0 || cursor != 0 {
		t.Errorf("respuesta vacía inesperada: %d cambios, cursor %d", len(changes), cursor)
	}
}

func TestPushServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("token inválido"))
	}))
	defer server.Close()

	cl := NewClient(server.URL)
	err := cl.Push(context.Background(), "memory", "tok", []ports.Change{{Key: "x", Op: domain.SyncOpUpsert, Payload: []byte(`{}`)}})
	if err == nil {
		t.Fatal("un 401 debe propagarse como error")
	}
}

func TestPullServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cl := NewClient(server.URL)
	if _, _, err := cl.Pull(context.Background(), "memory", "tok", 0); err == nil {
		t.Error("un 500 debe propagarse como error")
	}
}

func TestPathEscapesTarget(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_ = json.NewEncoder(w).Encode(pullResponse{})
	}))
	defer server.Close()

	cl := NewClient(server.URL)
	if _, _, err := cl.Pull(context.Background(), "memory", "a/b token", 0); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if gotPath != "/memory/a%2Fb%20token/changes" {
		t.Errorf("el target no se escapó en la ruta: %s", gotPath)
	}
}
