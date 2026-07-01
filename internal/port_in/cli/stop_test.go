package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// portOf extrae el puerto de la URL de un httptest.Server (formato http://127.0.0.1:PUERTO).
func portOf(t *testing.T, url string) string {
	t.Helper()
	_, port, ok := strings.Cut(strings.TrimPrefix(url, "http://"), ":")
	if !ok {
		t.Fatalf("no se pudo extraer el puerto de %q", url)
	}
	return port
}

// TestStopStopsRunningDaemon: con un daemon vivo, `kandel stop` pide /shutdown y, cuando /health deja
// de responder OK, reporta "detenido".
func TestStopStopsRunningDaemon(t *testing.T) {
	var down atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, _ *http.Request) {
		down.Store(true) // desde aquí /health responde caído, como haría el proceso al cerrar
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		if down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	t.Setenv("KANDEL_PORT", portOf(t, ts.URL))

	var out bytes.Buffer
	app := &App{Out: &out}
	if err := (stopCmd{}).Run(context.Background(), nil, app); err != nil {
		t.Fatalf("stop devolvió error: %v", err)
	}
	if !strings.Contains(out.String(), "detenido") {
		t.Fatalf("se esperaba 'detenido', salida = %q", out.String())
	}
}

// TestStopWhenNotRunning: sin daemon escuchando, `kandel stop` no falla y reporta "no estaba corriendo".
func TestStopWhenNotRunning(t *testing.T) {
	ts := httptest.NewServer(http.NewServeMux())
	port := portOf(t, ts.URL)
	ts.Close() // libera el puerto → la conexión será rechazada

	t.Setenv("KANDEL_PORT", port)

	var out bytes.Buffer
	app := &App{Out: &out}
	if err := (stopCmd{}).Run(context.Background(), nil, app); err != nil {
		t.Fatalf("stop no debería fallar si el daemon está caído: %v", err)
	}
	if !strings.Contains(out.String(), "no estaba corriendo") {
		t.Fatalf("se esperaba 'no estaba corriendo', salida = %q", out.String())
	}
}
