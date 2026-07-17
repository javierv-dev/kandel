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

	"github.com/javierv-dev/kandel/internal/project"
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
	wantProject := project.Normalize(filepath.Base(cwd))
	if req.body["project"] != wantProject {
		t.Errorf("project = %v, se esperaba %q", req.body["project"], wantProject)
	}
	if !strings.Contains(out.String(), "Memoria persistente kandel") {
		t.Errorf("la salida no contiene el protocolo:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "CONTEXTO DE PRUEBA") {
		t.Errorf("la salida no inyecta el contexto:\n%s", out.String())
	}
	// Metadatos internos: project + session_id, para que el agente los pase a mem_save.
	if s := out.String(); !strings.Contains(s, "uso interno kandel") || !strings.Contains(s, "session_id: sess-1") {
		t.Errorf("la salida no inyecta los metadatos internos:\n%s", s)
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
	// Sin session_id, el bloque de metadatos NO debe listar esa clave (se omite lo vacío), pero sí
	// el project (el basename del cwd).
	if s := out.String(); strings.Contains(s, "session_id:") {
		t.Errorf("sin session_id no debe aparecer esa clave en los metadatos:\n%s", s)
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

// fakeRelease cumple ports.ReleaseChecker para el test del banner de versión nueva.
type fakeRelease struct {
	latest string
	newer  bool
}

func (f fakeRelease) Newer(context.Context, string) (string, bool) { return f.latest, f.newer }

func TestSessionStartUpdateBanner(t *testing.T) {
	d := newStubDaemon(t)

	// Hay versión nueva → banner presente.
	r, out := newTestRunner(t, "session-start", d)
	r.running = "v1.0.0"
	r.updates = fakeRelease{latest: "v1.1.0", newer: true}
	if err := r.sessionStart(payload{SessionID: "sess-up", CWD: t.TempDir()}); err != nil {
		t.Fatalf("sessionStart: %v", err)
	}
	if s := out.String(); !strings.Contains(s, "versión nueva de kandel") || !strings.Contains(s, "v1.1.0") {
		t.Errorf("faltó el banner de versión nueva:\n%s", s)
	}

	// Al día → sin banner.
	r2, out2 := newTestRunner(t, "session-start", d)
	r2.running = "v1.1.0"
	r2.updates = fakeRelease{latest: "v1.1.0", newer: false}
	if err := r2.sessionStart(payload{SessionID: "sess-ok2", CWD: t.TempDir()}); err != nil {
		t.Fatalf("sessionStart: %v", err)
	}
	if strings.Contains(out2.String(), "versión nueva de kandel") {
		t.Errorf("no debe haber banner estando al día:\n%s", out2.String())
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
	if !strings.Contains(s, "post-compactación") || !strings.Contains(s, "mem_context") {
		t.Errorf("falta el protocolo post-compactación:\n%s", s)
	}
	if !strings.Contains(s, "CONTEXTO DE PRUEBA") {
		t.Errorf("post-compactación debe inyectar el contexto:\n%s", s)
	}
	// La compactación borró el contexto previo → hay que re-inyectar los metadatos internos.
	if !strings.Contains(s, "session_id: sc-1") {
		t.Errorf("post-compactación debe re-inyectar el session_id:\n%s", s)
	}
}

func TestAgentMetadataRender(t *testing.T) {
	// Ambos valores presentes → bloque con las dos claves.
	full := agentMetadata{project: "kandel", sessionID: "sess-9"}.render()
	if !strings.Contains(full, "project: kandel") || !strings.Contains(full, "session_id: sess-9") {
		t.Errorf("render completo incorrecto:\n%s", full)
	}
	// session_id vacío → se omite esa clave (la línea "session_id:"), pero el project queda. Se busca
	// la clave con dos puntos porque la prosa de instrucción menciona la palabra "session_id".
	partial := agentMetadata{project: "kandel", sessionID: "  "}.render()
	if strings.Contains(partial, "session_id:") || !strings.Contains(partial, "project: kandel") {
		t.Errorf("render parcial debe omitir session_id vacío:\n%s", partial)
	}
	// Sin ningún dato → bloque vacío (el llamador no imprime nada).
	if empty := (agentMetadata{}).render(); empty != "" {
		t.Errorf("render sin datos debe ser vacío, fue:\n%s", empty)
	}
}

func TestAgPreInvocationStartInjectsContext(t *testing.T) {
	d := newStubDaemon(t)
	r, out := newTestRunner(t, "ag-pre-invocation", d)
	cwd := t.TempDir() // sin git → proyecto = basename del cwd

	// invocationNum 0 = arranque: crea sesión e inyecta contexto como injectSteps.
	if err := r.agPreInvocation(agInput{ConversationID: "conv-1", WorkspacePaths: []string{cwd}, InvocationNum: 0}); err != nil {
		t.Fatalf("agPreInvocation: %v", err)
	}

	req := d.find(t, "POST", "/sessions")
	if req.body["id"] != "conv-1" {
		t.Errorf("id de sesión incorrecto: %v", req.body["id"])
	}
	// La salida debe ser JSON con injectSteps[0].ephemeralMessage conteniendo contexto y metadatos.
	var payload struct {
		InjectSteps []struct {
			EphemeralMessage string `json:"ephemeralMessage"`
		} `json:"injectSteps"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("la salida no es JSON de Antigravity: %v\n%s", err, out.String())
	}
	if len(payload.InjectSteps) != 1 {
		t.Fatalf("se esperaba 1 injectStep, hubo %d: %s", len(payload.InjectSteps), out.String())
	}
	msg := payload.InjectSteps[0].EphemeralMessage
	if !strings.Contains(msg, "CONTEXTO DE PRUEBA") {
		t.Errorf("el paso inyectado no trae el contexto:\n%s", msg)
	}
	if !strings.Contains(msg, "session_id: conv-1") {
		t.Errorf("el paso inyectado no trae los metadatos internos:\n%s", msg)
	}
	// No debe llevar la jerga de Claude (tools diferidas / ToolSearch), que en Antigravity no aplica.
	if strings.Contains(msg, "ToolSearch") {
		t.Errorf("el contexto de Antigravity no debe mencionar ToolSearch:\n%s", msg)
	}
}

func TestAgPreInvocationLaterTurnOnlyBackfills(t *testing.T) {
	d := newStubDaemon(t)
	r, out := newTestRunner(t, "ag-pre-invocation", d)

	// invocationNum > 0: no crea sesión ni inyecta contexto; solo dispara backfill (que el stub no
	// registra como sesión). La salida debe ser un objeto vacío (sin injectSteps).
	if err := r.agPreInvocation(agInput{ConversationID: "conv-2", WorkspacePaths: []string{t.TempDir()}, InvocationNum: 3}); err != nil {
		t.Fatalf("agPreInvocation: %v", err)
	}
	if n := d.count("POST", "/sessions"); n != 0 {
		t.Errorf("un turno posterior no debe crear sesión, hubo %d", n)
	}
	if s := strings.TrimSpace(out.String()); s != "{}" {
		t.Errorf("un turno posterior debe imprimir {}; salida: %q", s)
	}
}

func TestAgStopEndsSession(t *testing.T) {
	d := newStubDaemon(t)
	r, out := newTestRunner(t, "ag-stop", d)
	if err := r.agStop(agInput{ConversationID: "conv-9"}); err != nil {
		t.Fatalf("agStop: %v", err)
	}
	d.find(t, "POST", "/sessions/conv-9/end")
	// Debe permitir el cierre (decision != "continue").
	var payload struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("la salida de Stop no es JSON: %v", err)
	}
	if payload.Decision == "continue" {
		t.Errorf("Stop no debe forzar continue (bloquearía el cierre): %+v", payload)
	}
}

func TestParseAgInputTolerant(t *testing.T) {
	in := parseAgInput(strings.NewReader(`{"conversationId":"c","workspacePaths":["/x"],"invocationNum":2}`))
	if in.ConversationID != "c" || in.cwd() != "/x" || in.InvocationNum != 2 {
		t.Errorf("parseo incorrecto: %+v", in)
	}
	// Entrada vacía/basura → input en blanco, sin pánico; cwd() tolera la lista vacía.
	if got := parseAgInput(strings.NewReader("")); got.ConversationID != "" || got.cwd() != "" {
		t.Errorf("entrada vacía debe dar input en blanco")
	}
	if got := parseAgInput(strings.NewReader("no es json")); got.ConversationID != "" {
		t.Errorf("basura debe dar input en blanco")
	}
}

func TestParsePayloadTolerant(t *testing.T) {
	// JSON válido.
	p := parsePayload(strings.NewReader(`{"session_id":"a","cwd":"/x","prompt":"p"}`))
	if p.SessionID != "a" || p.CWD != "/x" || p.Prompt != "p" {
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
