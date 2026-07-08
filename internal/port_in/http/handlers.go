package http

import (
	"context"
	"log"
	"net/http"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/learnings"
	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/service"
)

// saveMemoryRequest es el cuerpo de POST /memories.
type saveMemoryRequest struct {
	SessionID  string `json:"session_id"`
	Project    string `json:"project"`
	Type       string `json:"type"`
	Scope      string `json:"scope"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	ToolName   string `json:"tool_name"`
	Resolution string `json:"resolution"`
}

// handleSaveMemory crea una memoria nueva.
func handleSaveMemory(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body saveMemoryRequest
		if !decodeBody(w, r, &body) {
			return
		}
		scope, err := domain.ParseScope(body.Scope)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		resolution := domain.Resolution(body.Resolution)
		if !resolution.Valid() {
			writeError(w, http.StatusBadRequest, "resolution inválida: usa open, resolved o vacío")
			return
		}
		res, err := svc.Memory.Save(r.Context(), service.SaveInput{
			SessionID:  body.SessionID,
			Project:    body.Project,
			Type:       domain.MemoryType(body.Type),
			Scope:      scope,
			Title:      body.Title,
			Content:    body.Content,
			ToolName:   body.ToolName,
			Resolution: resolution,
		})
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":                 res.Memory.ID,
			"outcome":            string(res.Outcome),
			"memory":             res.Memory,
			"embedding_deferred": res.EmbeddingDeferred,
		})
	}
}

// handleGetMemory devuelve una memoria por id.
func handleGetMemory(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		m, err := svc.Memory.Get(r.Context(), id)
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		if m == nil {
			writeError(w, http.StatusNotFound, "memoria no encontrada")
			return
		}
		writeJSON(w, http.StatusOK, m)
	}
}

// handleRecent lista memorias recientes (lectura directa).
func handleRecent(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var scope domain.Scope
		if raw := r.URL.Query().Get("scope"); raw != "" {
			s, err := domain.ParseScope(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			scope = s
		}
		memories, err := svc.Memory.Recent(r.Context(), ports.RecentQuery{
			Project: r.URL.Query().Get("project"),
			Scope:   scope,
			Limit:   queryInt(r, "limit", 20),
		})
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"memories": memories})
	}
}

// updateMemoryRequest es el cuerpo de PATCH /memories/{id}. Los punteros nil no se tocan.
type updateMemoryRequest struct {
	Type       *string `json:"type"`
	Title      *string `json:"title"`
	Content    *string `json:"content"`
	Scope      *string `json:"scope"`
	Resolution *string `json:"resolution"`
}

// handleUpdateMemory aplica cambios parciales a una memoria.
func handleUpdateMemory(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var body updateMemoryRequest
		if !decodeBody(w, r, &body) {
			return
		}
		var in service.UpdateInput
		if body.Title != nil {
			in.Title = body.Title
		}
		if body.Content != nil {
			in.Content = body.Content
		}
		if body.Type != nil {
			t := domain.MemoryType(*body.Type)
			in.Type = &t
		}
		if body.Scope != nil {
			s, err := domain.ParseScope(*body.Scope)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			in.Scope = &s
		}
		if body.Resolution != nil {
			res := domain.Resolution(*body.Resolution)
			if !res.Valid() {
				writeError(w, http.StatusBadRequest, "resolution inválida: usa open, resolved o vacío")
				return
			}
			in.Resolution = &res
		}
		m, err := svc.Memory.Update(r.Context(), id, in)
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, m)
	}
}

// handleDeleteMemory borra una memoria de forma lógica (soft-delete, reversible). No hay borrado
// físico: toda eliminación es reversible con el restore.
func handleDeleteMemory(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if err := svc.Memory.Delete(r.Context(), id); err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleSearch es la búsqueda semántica del agente.
func handleSearch(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		if query == "" {
			writeError(w, http.StatusBadRequest, "el parámetro q (consulta) es obligatorio")
			return
		}
		var scope domain.Scope
		if raw := r.URL.Query().Get("scope"); raw != "" {
			s, err := domain.ParseScope(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			scope = s
		}
		results, err := svc.Memory.Search(r.Context(), ports.SearchQuery{
			Query:   query,
			Type:    domain.MemoryType(r.URL.Query().Get("type")),
			Project: r.URL.Query().Get("project"),
			Scope:   scope,
			Limit:   queryInt(r, "limit", 10),
		})
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	}
}

// handlePending lista las memorias pendientes por resolver (resolution = open).
func handlePending(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		memories, err := svc.Lifecycle.PendingResolution(r.Context(), r.URL.Query().Get("project"), queryInt(r, "limit", 20))
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"memories": memories})
	}
}

// startSessionRequest es el cuerpo de POST /sessions.
type startSessionRequest struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	Directory string `json:"directory"`
}

// handleStartSession abre una sesión.
func handleStartSession(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body startSessionRequest
		if !decodeBody(w, r, &body) {
			return
		}
		if body.ID == "" {
			writeError(w, http.StatusBadRequest, "id es obligatorio")
			return
		}
		s, err := svc.Session.Start(r.Context(), service.StartInput{
			ID:        body.ID,
			Project:   body.Project,
			Directory: body.Directory,
		})
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, s)
	}
}

// handleEndSession cierra una sesión (marca su instante de cierre). No lleva cuerpo.
func handleEndSession(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := svc.Session.End(r.Context(), id); err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "ended": true})
	}
}

// handleGetSession devuelve una sesión por id.
func handleGetSession(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := svc.Session.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		if s == nil {
			writeError(w, http.StatusNotFound, "sesión no encontrada")
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}

// savePromptRequest es el cuerpo de POST /prompts.
type savePromptRequest struct {
	SessionID string `json:"session_id"`
	Project   string `json:"project"`
	Content   string `json:"content"`
}

// handleSavePrompt guarda un prompt (contexto crudo).
func handleSavePrompt(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body savePromptRequest
		if !decodeBody(w, r, &body) {
			return
		}
		res, err := svc.Prompt.Save(r.Context(), service.SavePromptInput{
			SessionID: body.SessionID,
			Project:   body.Project,
			Content:   body.Content,
		})
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": res.Prompt.ID, "inserted": res.Inserted})
	}
}

// handleContext arma el bloque de contexto del proyecto (sesiones, prompts y memorias recientes)
// que los hooks inyectan al agente al iniciar la sesión.
func handleContext(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var scope domain.Scope
		if raw := r.URL.Query().Get("scope"); raw != "" {
			s, err := domain.ParseScope(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			scope = s
		}
		text, err := svc.Context.Format(r.Context(), r.URL.Query().Get("project"), scope)
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"context": text})
	}
}

// passiveCaptureRequest es el cuerpo de POST /memories/passive: la salida cruda de un agente
// (p. ej. de un subagente al terminar) de la que se extraen aprendizajes.
type passiveCaptureRequest struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	Project   string `json:"project"`
	Source    string `json:"source"`
}

// handlePassiveCapture extrae los aprendizajes en viñetas del contenido y guarda cada uno como
// una memoria de tipo discovery. El dedupe lo hace Memory.Save (por hash); aquí se cuentan los
// resultados. Es fire-and-forget desde el hook: nunca debe fallar la sesión del agente.
func handlePassiveCapture(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body passiveCaptureRequest
		if !decodeBody(w, r, &body) {
			return
		}
		items := learnings.Extract(body.Content)
		extracted := len(items)
		saved, duplicates := 0, 0
		for _, text := range items {
			res, err := svc.Memory.Save(r.Context(), service.SaveInput{
				SessionID: body.SessionID,
				Project:   body.Project,
				Type:      domain.TypeDiscovery,
				Scope:     domain.ScopeProject,
				Title:     learnings.Truncate(text, 80),
				Content:   text,
			})
			if err != nil {
				// Un fallo (p. ej. embebedor caído) corta: se informa lo guardado hasta aquí.
				writeError(w, statusForError(err), err.Error())
				return
			}
			switch res.Outcome {
			case service.SaveCreated:
				saved++
			default:
				// merged o duplicate: ya existía algo equivalente.
				duplicates++
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"extracted":  extracted,
			"saved":      saved,
			"duplicates": duplicates,
		})
	}
}

// migrateProjectRequest es el cuerpo de POST /projects/migrate.
type migrateProjectRequest struct {
	OldProject string `json:"old_project"`
	NewProject string `json:"new_project"`
}

// handleMigrateProject renombra un proyecto en todas sus entidades. Es idempotente: nombres que
// coinciden tras normalizar, o sin filas que migrar, responden 200 con status "skipped".
func handleMigrateProject(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body migrateProjectRequest
		if !decodeBody(w, r, &body) {
			return
		}
		if body.OldProject == "" || body.NewProject == "" {
			writeError(w, http.StatusBadRequest, "old_project y new_project son obligatorios")
			return
		}
		res, err := svc.Project.Migrate(r.Context(), body.OldProject, body.NewProject)
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		if !res.Migrated {
			writeJSON(w, http.StatusOK, map[string]any{"status": "skipped"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":   "migrated",
			"memories": res.MemoriesUpdated,
			"sessions": res.SessionsUpdated,
			"prompts":  res.PromptsUpdated,
		})
	}
}

// handleBackfill dispara un backfill ASÍNCRONO de las memorias guardadas sin vector (embebedor
// caído) y responde 202 de inmediato. Lo invoca el hook UserPromptSubmit para que los vectores
// queden al día cuanto antes sin esperar al barrido periódico. La goroutine usa un contexto propio
// (no el de la petición, que se cancela al responder) y la omisión por solapamiento la maneja
// SweepBackfill (si ya hay uno en curso, no encola otro).
func handleBackfill(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if svc.Embedding == nil {
			writeError(w, http.StatusServiceUnavailable, "embebedor no disponible")
			return
		}
		go func() {
			res, skipped, err := svc.Embedding.SweepBackfill(context.Background())
			switch {
			case err != nil:
				log.Printf("backfill (disparado): error tras %d memorias: %v", res.Embedded, err)
			case skipped:
				log.Printf("backfill (disparado): omitido, ya había uno en curso")
			case res.Embedded > 0:
				log.Printf("backfill (disparado): %d memoria(s) embebida(s)", res.Embedded)
			}
		}()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "backfill lanzado"})
	}
}

// handleSyncPush dispara un push ASÍNCRONO (sube lo local al hub) y responde 202 de inmediato. Lo
// invoca el Stop hook para no colgar la terminal: un push puede tardar por red, así que se delega al
// daemon (mismo patrón que el backfill). El guard de exclusión de AutoPush omite el disparo si ya hay
// un auto-sync en curso; la cola local es la verdad, así que lo no enviado se reintenta al siguiente.
func handleSyncPush(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if svc.Sync == nil {
			writeError(w, http.StatusServiceUnavailable, "sync no disponible")
			return
		}
		go func() {
			res, skipped, err := svc.Sync.AutoPush(context.Background())
			switch {
			case err != nil:
				log.Printf("sync push (disparado): error: %v", err)
			case skipped:
				log.Printf("sync push (disparado): omitido, ya había un sync en curso")
			case res.Count > 0:
				log.Printf("sync push (disparado): %d fila(s) subida(s)", res.Count)
			}
		}()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "push lanzado"})
	}
}

// handleSyncPull dispara un pull ASÍNCRONO (baja lo ajeno del hub) y responde 202 de inmediato. Lo
// invoca el SessionStart hook. Es asíncrono a propósito: un arranque en frío puede traer mucho estado
// y no debe bloquear el inicio de la sesión. Consecuencia honesta: lo recién bajado puede no entrar en
// el contexto de ESTA sesión (llega al vuelo), pero sí en la siguiente y lo cubre el timer.
func handleSyncPull(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if svc.Sync == nil {
			writeError(w, http.StatusServiceUnavailable, "sync no disponible")
			return
		}
		go func() {
			res, skipped, err := svc.Sync.AutoPull(context.Background())
			switch {
			case err != nil:
				log.Printf("sync pull (disparado): error: %v", err)
			case skipped:
				log.Printf("sync pull (disparado): omitido, ya había un sync en curso")
			case res.Applied > 0 || res.Conflicts > 0:
				log.Printf("sync pull (disparado): %d aplicada(s), %d conflicto(s)", res.Applied, res.Conflicts)
			}
		}()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pull lanzado"})
	}
}

// handleWarmup dispara un warm-up ASÍNCRONO del modelo de embeddings (una petición "ping") y
// responde 202 de inmediato. Lo invoca el hook SessionStart para que el modelo quede cargado en
// memoria y el primer Save real no pague el arranque en frío (~7s) de Ollama.
func handleWarmup(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if svc.Embedding == nil {
			writeError(w, http.StatusServiceUnavailable, "embebedor no disponible")
			return
		}
		go func() {
			if err := svc.Embedding.Warmup(context.Background()); err != nil {
				log.Printf("warm-up del embebedor: falló (Ollama caído?): %v", err)
			}
		}()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "warm-up lanzado"})
	}
}
