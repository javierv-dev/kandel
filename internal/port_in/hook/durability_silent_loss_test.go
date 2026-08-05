package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Fase 5 de la auditoría de durabilidad (docs/auditoria-durabilidad.md) ---
//
// La Fase 1 dejó acotado el problema: kandel no corrompe datos, los RECHAZA, y lo hace devolviendo
// un error visible. Por tanto todo depende de qué hace quien llama con ese error. Esta fase mide
// exactamente eso en la capa determinista (los hooks), que es la que la arquitectura da por
// garantizada: "graba sesiones y prompts SIEMPRE, sin depender del agente".
//
// La cadena bajo examen:
//
//  1. El daemon (kandel serve) es un net/http normal: una goroutine por petición, es decir,
//     escrituras CONCURRENTES DENTRO DE UN MISMO PROCESO — el escenario que la Fase 1 midió con
//     20-25% de éxito.
//  2. Si la escritura falla, statusForError la manda al caso por defecto: HTTP 500.
//  3. El hook hace `code, _ := r.client.savePrompt(...)`: descarta el error Y no comprueba el
//     código. Solo lo escribe en el log.
//
// Estos tests miden el eslabón 3, que es donde un fallo visible se vuelve invisible.

// failingDaemon es un daemon simulado que responde con el código indicado a POST /prompts,
// imitando a un daemon que no pudo escribir en la base.
func failingDaemon(t *testing.T, status int) (*httptest.Server, *int32Counter) {
	t.Helper()
	intentos := &int32Counter{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /prompts", func(w http.ResponseWriter, _ *http.Request) {
		intentos.inc()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"sqlite: encolar prompt: database is locked (5) (SQLITE_BUSY)"}`))
	})
	// El resto de endpoints que toca userPromptSubmit responden normal: se quiere aislar el fallo
	// del guardado del prompt, no provocar un apagón general.
	mux.HandleFunc("POST /embeddings/backfill", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /relevant", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"context": "", "ids": []int64{}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, intentos
}

// int32Counter es un contador seguro para usar desde los manejadores HTTP.
type int32Counter struct {
	mu sync.Mutex
	n  int
}

func (c *int32Counter) inc() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

func (c *int32Counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// TestDurabilityHookSwallowsFailedPromptSave comprueba qué percibe el sistema cuando el daemon
// rechaza el guardado del prompt con HTTP 500.
//
// Cuando se escribió (Fase 5) medía el defecto: el hook devolvía nil, imprimía su JSON habitual y
// terminaba con éxito, sin más rastro que una línea indistinguible en el log. Tras la corrección 2
// es la regresión de lo contrario: el hook sigue sin fallar (no puede), pero el fallo queda
// marcado como ERROR y la escritura se reintenta.
func TestDurabilityHookSwallowsFailedPromptSave(t *testing.T) {
	srv, intentos := failingDaemon(t, http.StatusInternalServerError)
	logDirTmp := t.TempDir()
	t.Setenv("KANDEL_LOG_DIR", logDirTmp)

	out := &bytes.Buffer{}
	r := &runner{
		event:  "user-prompt-submit",
		client: &client{base: srv.URL, hc: &http.Client{Timeout: 2 * time.Second}},
		out:    out,
		ctx:    context.Background(),
	}

	err := r.userPromptSubmit(payload{
		SessionID: "sesion-fase5",
		CWD:       t.TempDir(),
		Prompt:    "un prompt que el usuario da por guardado",
	})

	// El daemon recibió el intento inicial más los reintentos de una escritura crítica.
	if intentos.get() < 1 {
		t.Fatalf("el daemon no recibió ningún intento de guardado")
	}
	t.Logf("intentos de guardado recibidos por el daemon: %d (1 inicial + reintentos)", intentos.get())

	t.Logf("valor de retorno del hook: %v", err)
	t.Logf("stdout del hook: %q", out.String())

	// El hook debe seguir devolviendo nil: un exit≠0 bloquearía el mensaje del usuario en el
	// agente. Esa restricción no se toca; lo que cambia es que el fallo deje rastro distinguible.
	if err != nil {
		t.Errorf("el hook devolvió error (%v); un hook nunca debe fallar: bloquearía al usuario", err)
	}

	// La salida hacia el agente sigue siendo la normal, y debe serlo: inyectar avisos ahí
	// contaminaría el contexto del modelo en cada fallo transitorio.
	t.Logf("salida hacia el agente: %d bytes (igual que en un guardado correcto)", out.Len())

	// PORTÓN: el fallo tiene que quedar registrado de forma DISTINGUIBLE, no diluido entre las
	// líneas normales. Es la señal máxima compatible con "un hook nunca termina con exit≠0".
	rastro, leido := leerLog(t, filepath.Join(logDirTmp, "hooks.log"))
	if !leido {
		t.Fatal("no se escribió el log de hooks: el fallo no deja NINGÚN rastro")
	}
	t.Logf("rastro en %s:\n%s", filepath.Join(logDirTmp, "hooks.log"), strings.TrimSpace(rastro))

	if !strings.Contains(rastro, "500") {
		t.Errorf("el log no registra el código de fallo; contenido:\n%s", rastro)
	}
	if !strings.Contains(rastro, "ERROR") {
		t.Errorf("el fallo no se marca como ERROR: sigue siendo indistinguible de una línea "+
			"normal; contenido:\n%s", rastro)
	}
}

// TestDurabilitySessionStartSwallowsFailedSession comprueba el mismo patrón en el arranque de
// sesión. Es más grave que el del prompt: si la sesión no se crea, los prompts posteriores cuelgan
// de un session_id que no existe, y como la clave foránea tampoco se aplica (H1), entran igual y
// quedan huérfanos.
func TestDurabilitySessionStartSwallowsFailedSession(t *testing.T) {
	logDirTmp := t.TempDir()
	t.Setenv("KANDEL_LOG_DIR", logDirTmp)

	intentos := &int32Counter{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /sessions", func(w http.ResponseWriter, _ *http.Request) {
		intentos.inc()
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("POST /embeddings/warmup", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /context", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"context": ""})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	out := &bytes.Buffer{}
	r := &runner{
		event:  "session-start",
		client: &client{base: srv.URL, hc: &http.Client{Timeout: 2 * time.Second}},
		out:    out,
		ctx:    context.Background(),
	}

	err := r.sessionStart(payload{SessionID: "sesion-fase5-b", CWD: t.TempDir()})

	if intentos.get() < 1 {
		t.Fatal("el daemon no recibió ningún intento de creación de sesión")
	}
	t.Logf("intentos de creación de sesión recibidos: %d (1 inicial + reintentos)", intentos.get())
	if err != nil {
		t.Errorf("sessionStart devolvió error (%v); un hook nunca debe fallar", err)
	}

	rastro, leido := leerLog(t, filepath.Join(logDirTmp, "hooks.log"))
	if !leido {
		t.Fatal("no se escribió el log de hooks: el fallo no deja NINGÚN rastro")
	}
	t.Logf("rastro:\n%s", strings.TrimSpace(rastro))
	if !strings.Contains(rastro, "ERROR") {
		t.Errorf("el fallo de creación de sesión no se marca como ERROR; contenido:\n%s", rastro)
	}
}

// TestDurabilityPostJSONSurfacesServerErrors cubre la raíz del problema en el cliente HTTP del
// hook. Antes, postJSON solo devolvía error si la petición NO LLEGABA: un rechazo del servidor
// volvía como (código, nil) y el `code, _ :=` de los llamadores descartaba lo único que podía
// delatar el fallo. Ahora cualquier código fuera de 2xx devuelve error.
func TestDurabilityPostJSONSurfacesServerErrors(t *testing.T) {
	casos := []struct {
		nombre string
		status int
	}{
		{"error interno", http.StatusInternalServerError},
		{"petición inválida", http.StatusBadRequest},
		{"no encontrado", http.StatusNotFound},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			srv, _ := failingDaemon(t, c.status)
			cl := &client{base: srv.URL, hc: &http.Client{Timeout: 2 * time.Second}}

			code, err := cl.savePrompt(context.Background(), "s", "contenido")
			t.Logf("HTTP %s -> code=%q err=%v", code, code, err)
			if err == nil {
				t.Errorf("un rechazo del servidor (HTTP %d) debe devolver error, devolvió nil", c.status)
			}
			if code != fmt.Sprint(c.status) {
				t.Errorf("code = %q, se esperaba %q", code, fmt.Sprint(c.status))
			}
		})
	}
}

// leerLog devuelve el contenido del log de hooks, o false si no existe.
func leerLog(t *testing.T, path string) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// TestDurabilityUserPromptSubmitAseguraLaSesion cubre la causa raíz del prompt huérfano.
//
// prepareStartup (SessionStart) sale sin crear la sesión si ensureDaemon falla, y el arranque en
// frío es justo cuando eso pasa: el daemon se lanza pero puede no responder dentro de los 3 s que
// se le esperan. Para cuando el usuario termina de escribir ya está arriba, y antes de esta
// corrección el prompt viajaba con un session_id que nunca se había creado.
//
// Se comprueba que el primer prompt de la sesión crea la sesión ANTES de guardar el prompt, y que
// los siguientes no repiten la llamada (postSession es idempotente, pero repetirla en cada mensaje
// sería una escritura por prompt a cambio de nada).
func TestDurabilityUserPromptSubmitAseguraLaSesion(t *testing.T) {
	t.Setenv("KANDEL_LOG_DIR", t.TempDir())

	var mu sync.Mutex
	var orden []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	registrar := func(nombre string, status int) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			orden = append(orden, nombre)
			mu.Unlock()
			w.WriteHeader(status)
		}
	}
	mux.HandleFunc("POST /sessions", registrar("sessions", http.StatusCreated))
	mux.HandleFunc("POST /prompts", registrar("prompts", http.StatusCreated))
	mux.HandleFunc("POST /embeddings/backfill", registrar("backfill", http.StatusAccepted))
	mux.HandleFunc("GET /relevant", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"context": "", "ids": []int64{}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// El session_id debe ser único por corrida: firstMessage deja su marcador en el temporal del
	// sistema y sobrevive entre ejecuciones de la suite.
	sesion := fmt.Sprintf("sesion-asegurada-%d", time.Now().UnixNano())
	cwd := t.TempDir()
	nuevoRunner := func() *runner {
		return &runner{
			event:  "user-prompt-submit",
			client: &client{base: srv.URL, hc: &http.Client{Timeout: 2 * time.Second}},
			out:    &bytes.Buffer{},
			ctx:    context.Background(),
		}
	}

	if err := nuevoRunner().userPromptSubmit(payload{
		SessionID: sesion, CWD: cwd, Prompt: "primer mensaje",
	}); err != nil {
		t.Fatalf("primer userPromptSubmit: %v", err)
	}

	mu.Lock()
	tras1 := append([]string(nil), orden...)
	mu.Unlock()
	t.Logf("llamadas del primer prompt: %v", tras1)

	if len(tras1) < 2 || tras1[0] != "sessions" || tras1[1] != "prompts" {
		t.Fatalf("se esperaba crear la sesión ANTES de guardar el prompt; orden observado: %v", tras1)
	}

	// Segundo prompt de la MISMA sesión: no debe volver a crearla.
	if err := nuevoRunner().userPromptSubmit(payload{
		SessionID: sesion, CWD: cwd, Prompt: "segundo mensaje",
	}); err != nil {
		t.Fatalf("segundo userPromptSubmit: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	sesiones := 0
	for _, o := range orden {
		if o == "sessions" {
			sesiones++
		}
	}
	t.Logf("llamadas totales tras dos prompts: %v", orden)
	if sesiones != 1 {
		t.Errorf("POST /sessions se llamó %d veces; solo debe hacerlo en el primer mensaje", sesiones)
	}
}
