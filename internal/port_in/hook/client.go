package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// client habla con el daemon HTTP local de kandel (kandel serve). Centraliza la base de la URL y
// el cliente HTTP con timeouts cortos: un hook no puede colgarse esperando al daemon.
type client struct {
	base string
	hc   *http.Client
}

// newClient construye el cliente apuntando a 127.0.0.1:$KANDEL_PORT (def. 8765), el mismo puerto
// que fija el instalador y que usa el comando serve (único origen de verdad del puerto).
func newClient() *client {
	port := strings.TrimSpace(os.Getenv("KANDEL_PORT"))
	if port == "" {
		port = "8765"
	}
	return &client{
		base: "http://127.0.0.1:" + port,
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
	_, _ = io.Copy(io.Discard, resp.Body)
	return fmt.Sprintf("%d", resp.StatusCode), nil
}

// postSession crea (o reactiva) una sesión.
func (c *client) postSession(ctx context.Context, id, project, directory string) (string, error) {
	return c.postJSON(ctx, "/sessions", map[string]string{"id": id, "project": project, "directory": directory})
}

// endSession marca una sesión como cerrada.
func (c *client) endSession(ctx context.Context, id string) (string, error) {
	return c.postJSON(ctx, "/sessions/"+url.PathEscape(id)+"/end", map[string]string{})
}

// savePrompt persiste un prompt del usuario.
func (c *client) savePrompt(ctx context.Context, sessionID, content string) (string, error) {
	return c.postJSON(ctx, "/prompts", map[string]string{"session_id": sessionID, "content": content})
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

// passiveCapture envía la salida cruda de un agente para extraer aprendizajes (captura pasiva).
func (c *client) passiveCapture(ctx context.Context, sessionID, content, project, source string) (string, error) {
	return c.postJSON(ctx, "/memories/passive", map[string]string{
		"session_id": sessionID, "content": content, "project": project, "source": source,
	})
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
