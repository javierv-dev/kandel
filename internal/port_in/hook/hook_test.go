package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// capturedRequest guarda lo esencial de una petición que recibió el daemon simulado.
type capturedRequest struct {
	method string
	path   string
	body   map[string]any
}

// stubDaemon es un daemon HTTP simulado: responde /health 200, /context con un texto fijo y
// registra el resto de peticiones para que los tests verifiquen qué envió el hook.
type stubDaemon struct {
	srv         *httptest.Server
	contextText string

	mu       sync.Mutex
	requests []capturedRequest
}

func newStubDaemon(t *testing.T) *stubDaemon {
	t.Helper()
	d := &stubDaemon{contextText: "CONTEXTO DE PRUEBA"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /context", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"context": d.contextText})
	})
	record := func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		d.mu.Lock()
		d.requests = append(d.requests, capturedRequest{method: r.Method, path: r.URL.Path, body: body})
		d.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}
	mux.HandleFunc("POST /sessions", record)
	mux.HandleFunc("POST /sessions/{id}/end", record)
	mux.HandleFunc("POST /prompts", record)
	mux.HandleFunc("POST /projects/migrate", record)
	mux.HandleFunc("POST /memories/passive", record)
	d.srv = httptest.NewServer(mux)
	t.Cleanup(d.srv.Close)
	return d
}

func (d *stubDaemon) find(t *testing.T, method, pathPrefix string) capturedRequest {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, req := range d.requests {
		if req.method == method && strings.HasPrefix(req.path, pathPrefix) {
			return req
		}
	}
	t.Fatalf("no se registró ninguna petición %s %s; registradas: %+v", method, pathPrefix, d.requests)
	return capturedRequest{}
}

func (d *stubDaemon) count(method, pathPrefix string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, req := range d.requests {
		if req.method == method && strings.HasPrefix(req.path, pathPrefix) {
			n++
		}
	}
	return n
}

// newTestRunner arma un runner apuntando al daemon simulado, con los logs redirigidos a un temporal
// (para no ensuciar ~/.kandel/logs) y la salida capturada en un buffer.
func newTestRunner(t *testing.T, event string, d *stubDaemon) (*runner, *bytes.Buffer) {
	t.Helper()
	t.Setenv("KANDEL_LOG_DIR", t.TempDir())
	out := &bytes.Buffer{}
	r := &runner{
		event:  event,
		client: &client{base: d.srv.URL, hc: &http.Client{Timeout: 2 * time.Second}},
		out:    out,
		ctx:    context.Background(),
	}
	return r, out
}

func TestSessionStartCreatesSessionAndInjectsContext(t *testing.T) {
	d := newStubDaemon(t)
	r, out := newTestRunner(t, "session-start", d)
	cwd := t.TempDir() // directorio sin git → el proyecto es su basename

	if err := r.sessionStart(payload{SessionID: "sess-1", CWD: cwd}); err != nil {
		t.Fatalf("sessionStart: %v", err)
	}

	req := d.find(t, "POST", "/sessions")
	if req.body["id"] != "sess-1" {
		t.Errorf("id de sesión incorrecto: %v", req.body["id"])
	}
	wantProject := normalize(filepath.Base(cwd))
	if req.body["project"] != wantProject {
		t.Errorf("project = %v, se esperaba %q", req.body["project"], wantProject)
	}
	if !strings.Contains(out.String(), "Memoria persistente kandel") {
		t.Errorf("la salida no contiene el protocolo:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "CONTEXTO DE PRUEBA") {
		t.Errorf("la salida no inyecta el contexto:\n%s", out.String())
	}
}

func TestSessionStartWithoutSessionIDDoesNotCreate(t *testing.T) {
	d := newStubDaemon(t)
	r, out := newTestRunner(t, "session-start", d)

	if err := r.sessionStart(payload{SessionID: "", CWD: t.TempDir()}); err != nil {
		t.Fatalf("sessionStart: %v", err)
	}
	if n := d.count("POST", "/sessions"); n != 0 {
		t.Errorf("sin session_id no debe crear sesión, hubo %d POST /sessions", n)
	}
	// Aun así inyecta el protocolo (es útil al modelo).
	if !strings.Contains(out.String(), "Memoria persistente kandel") {
		t.Errorf("debería imprimir el protocolo aunque no haya sesión")
	}
}

func TestSessionStartAssetMismatchBanner(t *testing.T) {
	d := newStubDaemon(t)

	// Desalineado: assets v1.0.0 vs binario v2.0.0 → banner presente.
	r, out := newTestRunner(t, "session-start", d)
	r.running = "v2.0.0"
	r.installedVersion = func() (string, bool, error) { return "v1.0.0", true, nil }
	if err := r.sessionStart(payload{SessionID: "sess-mm", CWD: t.TempDir()}); err != nil {
		t.Fatalf("sessionStart: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "kandel desactualizado") || !strings.Contains(s, "kandel install claude-code") {
		t.Errorf("faltó el banner de desalineación:\n%s", s)
	}

	// Alineado: misma versión → sin banner.
	r2, out2 := newTestRunner(t, "session-start", d)
	r2.running = "v2.0.0"
	r2.installedVersion = func() (string, bool, error) { return "v2.0.0", true, nil }
	if err := r2.sessionStart(payload{SessionID: "sess-ok", CWD: t.TempDir()}); err != nil {
		t.Fatalf("sessionStart: %v", err)
	}
	if strings.Contains(out2.String(), "kandel desactualizado") {
		t.Errorf("no debe haber banner con versiones alineadas:\n%s", out2.String())
	}

	// Sin instalación previa (present=false) → sin banner (no molesta).
	r3, out3 := newTestRunner(t, "session-start", d)
	r3.running = "v2.0.0"
	r3.installedVersion = func() (string, bool, error) { return "", false, nil }
	if err := r3.sessionStart(payload{SessionID: "sess-none", CWD: t.TempDir()}); err != nil {
		t.Fatalf("sessionStart: %v", err)
	}
	if strings.Contains(out3.String(), "kandel desactualizado") {
		t.Errorf("sin instalación no debe haber banner:\n%s", out3.String())
	}
}

func TestUserPromptSubmitSavesPromptAndFirstMessage(t *testing.T) {
	d := newStubDaemon(t)
	sid := fmt.Sprintf("sess-prompt-%d", time.Now().UnixNano())

	// Primer mensaje: guarda el prompt e inyecta la carga de tools.
	r1, out1 := newTestRunner(t, "user-prompt-submit", d)
	if err := r1.userPromptSubmit(payload{SessionID: sid, Prompt: "hola"}); err != nil {
		t.Fatalf("userPromptSubmit: %v", err)
	}
	req := d.find(t, "POST", "/prompts")
	if req.body["session_id"] != sid || req.body["content"] != "hola" {
		t.Errorf("cuerpo de /prompts incorrecto: %+v", req.body)
	}
	if !strings.Contains(out1.String(), "additionalContext") {
		t.Errorf("el primer mensaje debe inyectar additionalContext:\n%s", out1.String())
	}

	// Segundo mensaje de la MISMA sesión: ya no inyecta (estado por session_id).
	r2, out2 := newTestRunner(t, "user-prompt-submit", d)
	if err := r2.userPromptSubmit(payload{SessionID: sid, Prompt: "otra"}); err != nil {
		t.Fatalf("userPromptSubmit 2: %v", err)
	}
	if strings.TrimSpace(out2.String()) != "{}" {
		t.Errorf("el segundo mensaje debe imprimir {}; salida: %q", out2.String())
	}
}

func TestUserPromptSubmitEmptyDoesNotSave(t *testing.T) {
	d := newStubDaemon(t)
	r, out := newTestRunner(t, "user-prompt-submit", d)
	if err := r.userPromptSubmit(payload{SessionID: "", Prompt: ""}); err != nil {
		t.Fatalf("userPromptSubmit: %v", err)
	}
	if n := d.count("POST", "/prompts"); n != 0 {
		t.Errorf("sin prompt/sesión no debe guardar, hubo %d POST /prompts", n)
	}
	// Debe imprimir JSON válido siempre (si no, el agente bloquea el mensaje).
	if s := strings.TrimSpace(out.String()); s == "" {
		t.Errorf("debe imprimir JSON aunque no guarde nada")
	}
}

func TestSessionStopEndsSession(t *testing.T) {
	d := newStubDaemon(t)
	r, _ := newTestRunner(t, "session-stop", d)
	if err := r.sessionStop(payload{SessionID: "sess-9"}); err != nil {
		t.Fatalf("sessionStop: %v", err)
	}
	req := d.find(t, "POST", "/sessions/sess-9/end")
	if req.path != "/sessions/sess-9/end" {
		t.Errorf("ruta de cierre incorrecta: %s", req.path)
	}
}

func TestSubagentStopPassiveCapture(t *testing.T) {
	d := newStubDaemon(t)
	cwd := t.TempDir()

	// Con salida: envía la captura pasiva.
	r1, _ := newTestRunner(t, "subagent-stop", d)
	if err := r1.subagentStop(payload{SessionID: "s", CWD: cwd, Stdout: "Key Learnings:\n- algo"}); err != nil {
		t.Fatalf("subagentStop: %v", err)
	}
	req := d.find(t, "POST", "/memories/passive")
	if req.body["content"] != "Key Learnings:\n- algo" {
		t.Errorf("contenido de captura pasiva incorrecto: %+v", req.body)
	}
	if req.body["source"] != "subagent-stop" {
		t.Errorf("source incorrecto: %v", req.body["source"])
	}

	// Sin salida: no envía nada.
	r2, _ := newTestRunner(t, "subagent-stop", d)
	if err := r2.subagentStop(payload{SessionID: "s", CWD: cwd, Stdout: ""}); err != nil {
		t.Fatalf("subagentStop vacío: %v", err)
	}
	if n := d.count("POST", "/memories/passive"); n != 1 {
		t.Errorf("salida vacía no debe capturar; total capturas = %d", n)
	}
}

func TestPostCompactionInjectsProtocol(t *testing.T) {
	d := newStubDaemon(t)
	r, out := newTestRunner(t, "post-compaction", d)
	if err := r.postCompaction(payload{SessionID: "sc-1", CWD: t.TempDir()}); err != nil {
		t.Fatalf("postCompaction: %v", err)
	}
	if d.count("POST", "/sessions") != 1 {
		t.Errorf("post-compactación debe asegurar la sesión")
	}
	s := out.String()
	if !strings.Contains(s, "post-compactación") || !strings.Contains(s, "mem_session_summary") {
		t.Errorf("falta el protocolo post-compactación:\n%s", s)
	}
	if !strings.Contains(s, "CONTEXTO DE PRUEBA") {
		t.Errorf("post-compactación debe inyectar el contexto:\n%s", s)
	}
}

func TestParsePayloadTolerant(t *testing.T) {
	// JSON válido.
	p := parsePayload(strings.NewReader(`{"session_id":"a","cwd":"/x","prompt":"p","stdout":"o"}`))
	if p.SessionID != "a" || p.CWD != "/x" || p.Prompt != "p" || p.Stdout != "o" {
		t.Errorf("parseo incorrecto: %+v", p)
	}
	// Entrada vacía o basura → payload en blanco, sin pánico.
	if got := parsePayload(strings.NewReader("")); got.SessionID != "" {
		t.Errorf("entrada vacía debe dar payload en blanco")
	}
	if got := parsePayload(strings.NewReader("no es json")); got.SessionID != "" {
		t.Errorf("basura debe dar payload en blanco")
	}
}
