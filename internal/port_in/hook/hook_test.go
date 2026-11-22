package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/config"
	"github.com/javierv-dev/kandel/internal/project"
)

// capturedRequest guarda lo esencial de una petición que recibió el daemon simulado.
type capturedRequest struct {
	method string
	path   string
	body   map[string]any
	query  url.Values
}

// stubDaemon es un daemon HTTP simulado: responde /health 200, /context con un texto fijo y
// registra el resto de peticiones para que los tests verifiquen qué envió el hook.
type stubDaemon struct {
	srv         *httptest.Server
	contextText string

	mu          sync.Mutex
	requests    []capturedRequest
	relevantIDs []int64 // ids que /relevant devuelve como inyectados
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
		d.requests = append(d.requests, capturedRequest{method: r.Method, path: r.URL.Path, body: body, query: r.URL.Query()})
		d.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}
	mux.HandleFunc("POST /sessions", record)
	mux.HandleFunc("POST /sessions/{id}/end", record)
	mux.HandleFunc("POST /prompts", record)
	mux.HandleFunc("POST /projects/migrate", record)
	// /relevant registra la petición (para poder afirmar sobre exclude_session) y devuelve lo que el
	// test haya preparado en relevantIDs; sin nada preparado, no inyecta.
	mux.HandleFunc("GET /relevant", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.requests = append(d.requests, capturedRequest{method: r.Method, path: r.URL.Path, query: r.URL.Query()})
		ids := d.relevantIDs
		d.mu.Unlock()
		body := map[string]any{"context": "", "ids": ids}
		if len(ids) > 0 {
			body["context"] = "MEMORIA RELEVANTE"
		}
		_ = json.NewEncoder(w).Encode(body)
	})
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

func TestRunRegistersDistinctAgentSessions(t *testing.T) {
	d := newStubDaemon(t)
	u, err := url.Parse(d.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KANDEL_PORT", u.Port())
	t.Setenv("KANDEL_DATA_DIR", t.TempDir())
	t.Setenv("KANDEL_LOG_DIR", t.TempDir())
	cwd := t.TempDir()
	for _, tc := range []struct {
		event, app string
		input      any
	}{
		{"session-start", "claude-code", map[string]any{"session_id": "shared", "cwd": cwd}},
		{"ag-pre-invocation", "antigravity", map[string]any{"conversationId": "shared", "workspacePaths": []string{cwd}}},
		{"pi-session-start", "pi", map[string]any{"session_id": "shared", "cwd": cwd}},
	} {
		raw, err := json.Marshal(tc.input)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := Run(context.Background(), tc.event, "dev", nil, bytes.NewReader(raw), &output); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), tc.app+":shared") {
			t.Fatalf("metadatos: %s", &output)
		}
		d.mu.Lock()
		var match bool
		for _, r := range d.requests {
			match = match || (r.path == "/sessions" && r.body["id"] == tc.app+":shared" && r.body["application"] == tc.app && r.body["conversation_id"] == "shared")
		}
		d.mu.Unlock()
		if !match {
			t.Fatalf("no se registró el origen %s", tc.app)
		}
	}
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

// El filtro de sesión en curso: lo guardado en esta misma sesión no se reinyecta, porque el agente
// ya lo tiene delante en la conversación.
func TestUserPromptSubmitExcluyeLaSesionEnCurso(t *testing.T) {
	d := newStubDaemon(t)
	r, _ := newTestRunner(t, "user-prompt-submit", d)
	isolateTempDir(t)

	if err := r.userPromptSubmit(payload{SessionID: "sx-1", CWD: t.TempDir(), Prompt: "¿qué decidimos?"}); err != nil {
		t.Fatalf("userPromptSubmit: %v", err)
	}

	req := d.find(t, http.MethodGet, "/relevant")
	if got := req.query.Get("exclude_session"); got != "sx-1" {
		t.Errorf("exclude_session = %q, se esperaba sx-1", got)
	}
}

// La excepción: tras una compactación la conversación previa ya no está, así que lo guardado en la
// misma sesión vuelve a ser recuperable. Si el filtro siguiera activo, el agente perdería justo el
// contexto que la compactación acaba de borrar.
func TestUserPromptSubmitLevantaElFiltroTrasCompactar(t *testing.T) {
	d := newStubDaemon(t)
	isolateTempDir(t)
	cwd := t.TempDir()

	rc, _ := newTestRunner(t, "post-compaction", d)
	if err := rc.postCompaction(payload{SessionID: "sx-2", CWD: cwd}); err != nil {
		t.Fatalf("postCompaction: %v", err)
	}

	r, _ := newTestRunner(t, "user-prompt-submit", d)
	if err := r.userPromptSubmit(payload{SessionID: "sx-2", CWD: cwd, Prompt: "sigue donde estábamos"}); err != nil {
		t.Fatalf("userPromptSubmit: %v", err)
	}

	req := d.find(t, http.MethodGet, "/relevant")
	if got := req.query.Get("exclude_session"); got != "" {
		t.Errorf("tras compactar no debe filtrarse la sesión, exclude_session = %q", got)
	}
}

// El otro efecto de compactar: reiniciar el registro de ids ya inyectados. Sin esto el agente queda
// doblemente sin contexto —perdió la conversación y el dedup se niega a repetirle lo ya dado—.
func TestPostCompactionReiniciaElDedup(t *testing.T) {
	d := newStubDaemon(t)
	isolateTempDir(t)

	recordInjectedIDs("sx-3", []int64{7, 9})
	if len(loadInjectedIDs("sx-3")) != 2 {
		t.Fatal("precondición: los ids debían quedar registrados")
	}

	r, _ := newTestRunner(t, "post-compaction", d)
	if err := r.postCompaction(payload{SessionID: "sx-3", CWD: t.TempDir()}); err != nil {
		t.Fatalf("postCompaction: %v", err)
	}

	if ids := loadInjectedIDs("sx-3"); len(ids) != 0 {
		t.Errorf("el dedup debía reiniciarse al compactar, quedaron %v", ids)
	}
}

// isolateTempDir redirige os.TempDir a un directorio de la prueba, para que los marcadores por
// sesión (primer mensaje, ids inyectados, compactación) no se escriban en el temporal real ni se
// pisen entre pruebas. Hay que fijar las tres variables porque cada sistema mira la suya: TMPDIR en
// Unix, TMP y TEMP en Windows.
func isolateTempDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
}

// El bloque de arranque se paga en TODAS las sesiones, se use la memoria o no, así que su tamaño es
// un presupuesto, no un detalle. Partía de 6.620 caracteres; la doctrina ("qué guardar", "cómo
// clasificar el tipo", las reglas de la bitácora) se movió a las descripciones de las tools, que
// solo se cargan cuando el agente va a usarlas.
//
// El tope es holgado respecto al tamaño actual: no se trata de perseguir cada carácter, sino de que
// volver a volcar doctrina aquí falle en vez de pasar desapercibido.
func TestProtocoloDeArranqueSeMantieneCorto(t *testing.T) {
	const tope = 3000
	if n := len(protocolSessionStart); n > tope {
		t.Errorf("el protocolo de arranque mide %d caracteres (tope %d). Lo que sea 'cómo se usa mem_X' "+
			"va en la descripción de esa tool (internal/port_in/mcp/tools_memory.go), no aquí", n, tope)
	}
}

// Adelgazar no puede llevarse por delante lo que solo puede vivir aquí: cómo cargar las tools
// diferidas (sin eso el agente no puede llamar a ninguna), el disparador del guardado proactivo, la
// revisión de cierre y qué hacer con el contexto inyectado debajo.
func TestProtocoloDeArranqueConservaLoImprescindible(t *testing.T) {
	imprescindibles := []string{
		"ToolSearch select:mcp__kandel__mem_save", // sin esto no se puede llamar a ninguna tool
		"mem_save",                 // el disparador del guardado proactivo
		"supersedes",               // la revisión de cierre necesita saber cómo corregir
		"Bitácora — estado actual", // qué hacer con el contexto de abajo
		"Salud de memoria",
		"[uso interno kandel]", // de dónde saca el project/session_id obligatorios
	}
	for _, want := range imprescindibles {
		if !strings.Contains(protocolSessionStart, want) {
			t.Errorf("el protocolo de arranque ya no menciona %q", want)
		}
	}
}

func TestPiSessionStartPromptCompactionAndStop(t *testing.T) {
	d := newStubDaemon(t)
	d.relevantIDs = []int64{42}
	isolateTempDir(t)
	cwd := t.TempDir()

	// 1. piSessionStart crea sesión e inyecta protocolo de Pi + contexto + metadatos.
	rStart, outStart := newTestRunner(t, "pi-session-start", d)
	rStart.running = "v2.0.0"
	rStart.installedVersion = func() (string, bool, error) { return "v1.0.0", true, nil }
	if err := rStart.piSessionStart(payload{SessionID: "pi:sess-1", CWD: cwd}); err != nil {
		t.Fatalf("piSessionStart: %v", err)
	}
	if s := outStart.String(); !strings.Contains(s, "kandel install pi") || !strings.Contains(s, "tool_search") || !strings.Contains(s, "session_id: pi:sess-1") || !strings.Contains(s, "CONTEXTO DE PRUEBA") {
		t.Errorf("salida de piSessionStart incompleta:\n%s", s)
	}

	// 2. piPrompt guarda el prompt y devuelve JSON con additionalContext cuando hay memorias relevantes.
	rPrompt, outPrompt := newTestRunner(t, "pi-prompt", d)
	if err := rPrompt.piPrompt(payload{SessionID: "pi:sess-1", CWD: cwd, Prompt: "recordar arquitectura"}); err != nil {
		t.Fatalf("piPrompt: %v", err)
	}
	reqPrompt := d.find(t, "POST", "/prompts")
	if reqPrompt.body["session_id"] != "pi:sess-1" || reqPrompt.body["content"] != "recordar arquitectura" {
		t.Errorf("cuerpo de /prompts en piPrompt incorrecto: %+v", reqPrompt.body)
	}
	var resp struct {
		AdditionalContext string `json:"additionalContext"`
	}
	if err := json.Unmarshal(outPrompt.Bytes(), &resp); err != nil {
		t.Fatalf("salida de piPrompt no es JSON válido: %v\n%s", err, outPrompt.String())
	}
	if !strings.Contains(resp.AdditionalContext, "MEMORIA RELEVANTE") {
		t.Errorf("piPrompt no devolvió la memoria relevante: %+v", resp)
	}

	// 3. piStop cierra la sesión enviando transcript_path e ids inyectados.
	rStop, _ := newTestRunner(t, "pi-stop", d)
	if err := rStop.sessionStop(payload{SessionID: "pi:sess-1", TranscriptPath: "/tmp/pi-sess.jsonl"}); err != nil {
		t.Fatalf("piStop: %v", err)
	}
	reqStop := d.find(t, "POST", "/sessions/pi:sess-1/end")
	if reqStop.body["transcript_path"] != "/tmp/pi-sess.jsonl" {
		t.Errorf("piStop no envió transcript_path: %+v", reqStop.body)
	}
}

func TestSessionStartProjectMismatchBannerAndSuppression(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git no disponible")
	}
	d := newStubDaemon(t)
	dataDir := t.TempDir()
	t.Setenv("KANDEL_DATA_DIR", dataDir)
	t.Setenv(config.ProjectMismatchWarn.Env, "")

	// Creamos un repositorio cuya carpeta se llama "carpeta-local" pero cuyo git remote es "nombre-remoto".
	parent := t.TempDir()
	repoDir := filepath.Join(parent, "carpeta-local")
	if err := os.MkdirAll(filepath.Join(repoDir, "subcarpeta"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		if err := exec.Command("git", append([]string{"-C", repoDir}, args...)...).Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	runGit("init")
	runGit("remote", "add", "origin", "https://github.com/acme/nombre-remoto.git")

	// 1. Por defecto, al arrancar en el repo (incluso desde una subcarpeta), NO llama a POST /projects/migrate
	// y SÍ muestra el aviso de discrepancia comparando la carpeta raíz ("carpeta-local") contra el remoto ("nombre-remoto").
	r1, out1 := newTestRunner(t, "session-start", d)
	if err := r1.sessionStart(payload{SessionID: "sess-mis-1", CWD: filepath.Join(repoDir, "subcarpeta")}); err != nil {
		t.Fatalf("sessionStart: %v", err)
	}
	if n := d.count("POST", "/projects/migrate"); n != 0 {
		t.Errorf("no debe llamar a POST /projects/migrate automáticamente, hubo %d llamadas", n)
	}
	s1 := out1.String()
	if !strings.Contains(s1, "Discrepancia en el nombre del proyecto") ||
		!strings.Contains(s1, `"carpeta-local"`) ||
		!strings.Contains(s1, `"nombre-remoto"`) {
		t.Errorf("faltó el aviso de discrepancia esperado:\n%s", s1)
	}

	// 2. En una nueva conversación vuelve a mostrarse siempre por defecto.
	r2, out2 := newTestRunner(t, "pi-session-start", d)
	if err := r2.piSessionStart(payload{SessionID: "pi:sess-mis-2", CWD: repoDir}); err != nil {
		t.Fatalf("piSessionStart: %v", err)
	}
	if !strings.Contains(out2.String(), "Discrepancia en el nombre del proyecto") {
		t.Errorf("el aviso debe mostrarse en cada nueva sesión por defecto:\n%s", out2.String())
	}

	// 3. Cuando el usuario pide ocultar el aviso para ese proyecto, deja de mostrarse.
	if err := config.Save(dataDir, map[string]string{config.ProjectMismatchWarn.File: "nombre-remoto"}); err != nil {
		t.Fatal(err)
	}
	r3, out3 := newTestRunner(t, "session-start", d)
	if err := r3.sessionStart(payload{SessionID: "sess-mis-3", CWD: repoDir}); err != nil {
		t.Fatalf("sessionStart tras ocultar: %v", err)
	}
	if strings.Contains(out3.String(), "Discrepancia en el nombre del proyecto") {
		t.Errorf("el aviso debía ocultarse tras configurar project_mismatch_warn:\n%s", out3.String())
	}

	// 4. Cuando la carpeta raíz se llama igual que el remoto git, NO muestra aviso aunque se abra desde una subcarpeta.
	sameRepo := filepath.Join(parent, "nombre-remoto")
	if err := os.MkdirAll(filepath.Join(sameRepo, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", sameRepo, "init").Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", sameRepo, "remote", "add", "origin", "https://github.com/acme/nombre-remoto.git").Run(); err != nil {
		t.Fatal(err)
	}
	_ = config.Save(dataDir, map[string]string{config.ProjectMismatchWarn.File: ""})
	r4, out4 := newTestRunner(t, "session-start", d)
	if err := r4.sessionStart(payload{SessionID: "sess-same", CWD: filepath.Join(sameRepo, "internal")}); err != nil {
		t.Fatalf("sessionStart en subcarpeta: %v", err)
	}
	if strings.Contains(out4.String(), "Discrepancia en el nombre del proyecto") {
		t.Errorf("no debe haber aviso cuando la carpeta raíz coincide con el remoto git:\n%s", out4.String())
	}
}

func TestSystemAlertsBannerOnJudgeFailure(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("KANDEL_DATA_DIR", dataDir)
	t.Setenv("KANDEL_CONFLICT_MAX_ATTEMPTS", "3")

	d := newStubDaemon(t)
	cwd := t.TempDir()

	// Sin alertas -> sin banner.
	r0, out0 := newTestRunner(t, "session-start", d)
	if err := r0.sessionStart(payload{SessionID: "s-a0", CWD: cwd}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out0.String(), "Evento del sistema (juez de conflictos de kandel)") {
		t.Fatalf("no debe mostrar banner sin alertas:\n%s", out0.String())
	}

	// Registrar 3 fallos consecutivos (límite alcanzado -> Paused=true).
	for i := 0; i < 3; i++ {
		config.RecordJudgeFailure(dataDir, "claude", "haiku", fmt.Errorf(`llm: la CLI "claude" terminó con código 1: {"api_error":"usage_limit_reached","result":"You've hit your weekly limit"}`))
	}
	paused, alert := config.IsJudgePaused(dataDir, "claude", "haiku")
	if !paused || alert.Failures != 3 || !strings.Contains(alert.LastError, "You've hit your weekly limit") {
		t.Fatalf("alerta mal registrada: paused=%v alert=%+v", paused, alert)
	}

	// Debe inyectar el banner en Claude Code, Pi y Antigravity.
	r1, out1 := newTestRunner(t, "session-start", d)
	if err := r1.sessionStart(payload{SessionID: "s-a1", CWD: cwd}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out1.String(), "Evento del sistema (juez de conflictos de kandel)") ||
		!strings.Contains(out1.String(), "PAUSÓ en segundo plano") ||
		!strings.Contains(out1.String(), "You've hit your weekly limit") {
		t.Fatalf("debe mostrar banner con el fallo del juez en Claude Code:\n%s", out1.String())
	}

	rPi, outPi := newTestRunner(t, "pi-session-start", d)
	if err := rPi.piSessionStart(payload{SessionID: "pi:s-a1", CWD: cwd}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outPi.String(), "Evento del sistema (juez de conflictos de kandel)") {
		t.Fatalf("debe mostrar banner en Pi Agent:\n%s", outPi.String())
	}

	rAg, outAg := newTestRunner(t, "ag-pre-invocation", d)
	if err := rAg.agPreInvocation(agInput{ConversationID: "ag:s-a1", WorkspacePaths: []string{cwd}, InvocationNum: 0}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outAg.String(), "Evento del sistema (juez de conflictos de kandel)") {
		t.Fatalf("debe mostrar banner en Antigravity:\n%s", outAg.String())
	}

	// Dar de baja (dismiss) oculta el banner pero mantiene el juez pausado para ese agente/modelo.
	if err := config.DismissAlert(dataDir, "", false); err != nil {
		t.Fatal(err)
	}
	r2, out2 := newTestRunner(t, "session-start", d)
	if err := r2.sessionStart(payload{SessionID: "s-a2", CWD: cwd}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out2.String(), "Evento del sistema (juez de conflictos de kandel)") {
		t.Fatalf("no debe mostrar banner tras dismiss:\n%s", out2.String())
	}
	if pausedAfterDismiss, _ := config.IsJudgePaused(dataDir, "claude", "haiku"); !pausedAfterDismiss {
		t.Fatal("tras dismiss sin reset el juez debe seguir pausado para no volver a fallar en bucle")
	}
	// Pero si cambia a otro agente o modelo, ya no está pausado.
	if pausedPi, _ := config.IsJudgePaused(dataDir, "pi", "gemini-3-8-flash"); pausedPi {
		t.Fatal("un agente/modelo distinto no debe estar pausado")
	}
}
