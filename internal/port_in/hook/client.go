package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/javierv-dev/kandel/internal/config"
)

// client habla con el daemon HTTP local de kandel (kandel serve). Centraliza la base de la URL y
// el cliente HTTP con timeouts cortos: un hook no puede colgarse esperando al daemon.
type client struct {
	base string
	hc   *http.Client
}

// newClient construye el cliente apuntando a 127.0.0.1:<port>, con el puerto resuelto por config
// (env KANDEL_PORT > archivo > default 8765), el mismo origen de verdad que usa el comando serve.
func newClient() *client {
	return &client{
		base: "http://127.0.0.1:" + config.Resolve(config.Port),
		hc:   &http.Client{Timeout: 3 * time.Second},
	}
}

// health indica si el daemon responde a GET /health con 200.
func (c *client) health(ctx context.Context) bool {
	reqCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.base+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// ensureDaemon garantiza que el daemon está arriba: si no responde, lanza `kandel serve`
// desacoplado (sobrevive al hook) y sondea /health hasta ~3s. Registra cada paso en el log.
func (c *client) ensureDaemon(ctx context.Context, r *runner) error {
	if c.health(ctx) {
		return nil
	}
	r.klog(fmt.Sprintf("daemon caído; arrancando 'kandel serve' (log: %s)", filepathServeLog()))
	if err := spawnServe(); err != nil {
		r.klog("no se pudo arrancar 'kandel serve': " + err.Error())
		return err
	}
	for i := 0; i < 10; i++ {
		time.Sleep(300 * time.Millisecond)
		if c.health(ctx) {
			r.klog("daemon arriba en " + c.base)
			return nil
		}
	}
	r.klog("daemon NO responde tras 3s en " + c.base + " — revisa " + filepathServeLog())
	return fmt.Errorf("daemon no responde en %s", c.base)
}

// postJSON envía body como JSON a path por POST y devuelve el código HTTP como texto (para el log)
// o "sin-respuesta" si la petición ni siquiera llegó.
//
// Un código fuera del rango 2xx devuelve ERROR. Antes no lo hacía: solo fallaba si la petición no
// llegaba, así que un rechazo del servidor volvía como (código, nil) y todos los llamadores lo
// descartaban con `code, _ :=`. La auditoría de durabilidad midió el resultado: el daemon rechazaba
// el guardado con HTTP 500 y el hook terminaba con éxito, sin más rastro que una línea entre
// cientos en el log (docs/auditoria-durabilidad.md §5, hallazgo 5).
func (c *client) postJSON(ctx context.Context, path string, body any) (string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return "sin-respuesta", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return "sin-respuesta", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "sin-respuesta", err
	}
	defer resp.Body.Close()
	cuerpo, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	code := fmt.Sprintf("%d", resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return code, fmt.Errorf("%s %s: HTTP %s: %s", http.MethodPost, path, code,
			strings.TrimSpace(string(cuerpo)))
	}
	return code, nil
}

// intentosEscrituraCrítica es cuántas veces se reintenta una escritura que no se puede perder, y
// cuánto se espera entre intentos. El presupuesto total (~200 ms en el peor caso) está acotado a
// propósito: un hook corre delante del usuario y no puede colgarse.
var esperasReintento = []time.Duration{50 * time.Millisecond, 150 * time.Millisecond}

// esTransitorio indica si vale la pena reintentar. Un 5xx o un 429 suelen ser contención pasajera
// (la base ocupada, el daemon saturado) y el siguiente intento tiene buenas probabilidades. Un 4xx
// no: la petición está mal formada y repetirla solo gasta el tiempo del usuario. "sin-respuesta"
// significa que la petición ni salió —daemon arrancando, por ejemplo—, y eso sí se reintenta.
func esTransitorio(code string) bool {
	if code == "sin-respuesta" {
		return true
	}
	n, err := strconv.Atoi(code)
	if err != nil {
		return false
	}
	return n >= 500 || n == http.StatusTooManyRequests
}

// postCrítico envía una escritura que no debe perderse, reintentando ante un fallo transitorio.
//
// Es seguro reintentar porque las dos operaciones que lo usan son idempotentes en el servidor: la
// creación de sesión crea o reactiva, y el guardado de prompt no duplica uno idéntico de la misma
// sesión y proyecto. Un reintento de más no ensucia nada.
func (c *client) postCrítico(ctx context.Context, path string, body any) (string, error) {
	code, err := c.postJSON(ctx, path, body)
	for i := 0; err != nil && esTransitorio(code) && i < len(esperasReintento); i++ {
		select {
		case <-ctx.Done():
			return code, err
		case <-time.After(esperasReintento[i]):
		}
		code, err = c.postJSON(ctx, path, body)
	}
	return code, err
}

// postSession crea (o reactiva) una sesión. Escritura crítica: si no se crea, los prompts
// posteriores cuelgan de un session_id inexistente.
func (c *client) postSession(ctx context.Context, id, project, directory string) (string, error) {
	body := map[string]string{"id": id, "project": project, "directory": directory}
	if app, external, ok := strings.Cut(id, ":"); ok && (app == "claude-code" || app == "antigravity" || app == "pi") {
		body["application"], body["conversation_id"] = app, external
	}
	return c.postCrítico(ctx, "/sessions", body)
}

// endSession marca una sesión como cerrada y, de paso, le entrega al daemon lo que hace falta para
// medir si la memoria inyectada sirvió de algo: la ruta de la transcripción y los ids que se
// inyectaron durante la sesión. Ambos son opcionales; sin ellos la sesión se cierra igual.
//
// Los ids los aporta el hook porque es quien los tiene: el registro por sesión vive en un archivo
// temporal local (ver dedup.go), no en la base.
//
// No es crítica: si falla, la sesión queda abierta —defecto cosmético, no pérdida de datos— y esa
// medición se pierde.
func (c *client) endSession(ctx context.Context, id, transcriptPath string, injectedIDs []int64) (string, error) {
	return c.postJSON(ctx, "/sessions/"+url.PathEscape(id)+"/end", map[string]any{
		"transcript_path": transcriptPath,
		"injected_ids":    injectedIDs,
	})
}

// savePrompt persiste un prompt del usuario. Escritura crítica: es la razón de ser de la capa
// determinista, que promete registrar SIEMPRE lo que el usuario pidió.
func (c *client) savePrompt(ctx context.Context, sessionID, content string) (string, error) {
	return c.postCrítico(ctx, "/prompts", map[string]string{"session_id": sessionID, "content": content})
}

// migrateProject renombra un proyecto en todas sus entidades (idempotente en el servidor).
func (c *client) migrateProject(ctx context.Context, oldProject, newProject string) (string, error) {
	return c.postJSON(ctx, "/projects/migrate", map[string]string{"old_project": oldProject, "new_project": newProject})
}

// warmup pide al daemon que precaliente el modelo de embeddings (carga en RAM) de forma asíncrona,
// para que el primer Save de la sesión no pague el arranque en frío de Ollama (~7s).
func (c *client) warmup(ctx context.Context) (string, error) {
	return c.postJSON(ctx, "/embeddings/warmup", map[string]string{})
}

// triggerBackfill pide al daemon que reembeba (asíncrono) las memorias guardadas sin vector. Lo
// dispara el hook UserPromptSubmit para no esperar al barrido periódico del daemon.
func (c *client) triggerBackfill(ctx context.Context) (string, error) {
	return c.postJSON(ctx, "/embeddings/backfill", map[string]string{})
}

// triggerSyncPush pide al daemon que suba lo local al hub (asíncrono; responde 202). Lo dispara el
// Stop hook para no colgar la terminal esperando la red; el daemon hace el push de fondo.
func (c *client) triggerSyncPush(ctx context.Context) (string, error) {
	return c.postJSON(ctx, "/sync/push", map[string]string{})
}

// triggerSyncPull pide al daemon que baje lo ajeno del hub (asíncrono; responde 202). Lo dispara el
// SessionStart hook para traer lo de otras máquinas al empezar, sin bloquear el arranque de la sesión.
func (c *client) triggerSyncPull(ctx context.Context) (string, error) {
	return c.postJSON(ctx, "/sync/pull", map[string]string{})
}

// fetchContext recupera el bloque de contexto del proyecto (sesiones, prompts y memorias
// recientes). Devuelve "" si el daemon no responde o no hay contexto: el hook simplemente no
// inyecta nada en ese caso.
func (c *client) fetchContext(ctx context.Context, project string) string {
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	endpoint := c.base + "/context?project=" + url.QueryEscape(project)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return ""
	}
	var out struct {
		Context string `json:"context"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ""
	}
	return out.Context
}

// fetchRelevant recupera el bloque de memoria relevante para un prompt concreto (Capa 2: inyección
// dirigida por el prompt). Embebe y busca en el daemon; devuelve el texto formateado y los ids
// inyectados (para el dedup por sesión). exclude son ids ya inyectados antes en la sesión. Devuelve
// "" y nil si el daemon no responde, no hay coincidencia o falla el decode: el hook simplemente no
// inyecta nada en ese caso (degradación silenciosa, nunca bloquea el prompt).
// excludeSession, si no va vacío, omite lo guardado en esa sesión (ver el filtro de sesión en curso
// en userPromptSubmit).
func (c *client) fetchRelevant(ctx context.Context, project, prompt string, exclude []int64, excludeSession string) (string, []int64) {
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	endpoint := c.base + "/relevant?project=" + url.QueryEscape(project) + "&q=" + url.QueryEscape(prompt)
	if excludeSession != "" {
		endpoint += "&exclude_session=" + url.QueryEscape(excludeSession)
	}
	if len(exclude) > 0 {
		parts := make([]string, len(exclude))
		for i, id := range exclude {
			parts[i] = strconv.FormatInt(id, 10)
		}
		endpoint += "&exclude=" + url.QueryEscape(strings.Join(parts, ","))
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", nil
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", nil
	}
	var out struct {
		Context string  `json:"context"`
		IDs     []int64 `json:"ids"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", nil
	}
	return out.Context, out.IDs
}
