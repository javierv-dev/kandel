package http

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/javierv-dev/kandel/internal/domain"
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

// Defaults de la inyección por relevancia (Capa 2). El umbral es similitud de coseno [0..1]: solo
// entra lo que lo supera, de modo que un prompt sin memorias afines no inyecta nada. Configurable
// por config/query (ver server.Services).
//
// El 0.45 se calibró en la Fase 4 contra bge-m3 y se REVALIDÓ tras el cambio de embebedor por
// defecto a qwen3-embedding:0.6b, midiendo sobre la memoria real (187 memorias, 21 sondas
// relevantes que cubren los 6 tipos y 8 no relacionadas):
//
//	modelo y lado consulta                 ruido hasta   aciertos desde   margen del 0.45
//	qwen3-embedding + Instruct (actual)         0.3188           0.5236   +0.131 / +0.074
//	qwen3-embedding con consulta cruda          0.4696           0.4358   SOLAPAN
//	bge-m3 con consulta cruda                   0.4460           0.4972   +0.004 / +0.047
//
// Dos avisos que se desprenden de ahí. El primero: con qwen3 el 0.45 solo es válido PORQUE la
// consulta se embebe con instrucción (ports.Embedder.EmbedQuery); si alguien la embebiera cruda,
// no habría hueco y ningún umbral separaría el ruido de los aciertos. El segundo: quien siga en
// bge-m3 tiene un margen de apenas 0.004 sobre el ruido —la calibración original lo sobreestimó
// por medir con una sola sonda no relacionada—, así que ahí conviene subir el umbral por config.
const (
	relevantDefaultLimit     = 5
	relevantDefaultThreshold = 0.45
)

// handleRelevant arma el bloque de "memoria relevante" para un prompt concreto (lo consume el hook
// UserPromptSubmit). Embebe q, busca semántico sobre el proyecto y devuelve el CUERPO COMPLETO de
// lo que supera el umbral, excluyendo los ids ya inyectados en la sesión. Sin coincidencia sobre el
// umbral → context vacío (el hook no inyecta nada). Espeja handleSearch pero formatea para inyección.
func handleRelevant(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		if query == "" {
			writeError(w, http.StatusBadRequest, "el parámetro q (consulta) es obligatorio")
			return
		}
		// Línea base configurable (config del daemon); el parámetro de consulta tiene prioridad.
		baseLimit := relevantDefaultLimit
		if svc.RelevanceLimit > 0 {
			baseLimit = svc.RelevanceLimit
		}
		baseThreshold := relevantDefaultThreshold
		if svc.RelevanceThreshold > 0 {
			baseThreshold = svc.RelevanceThreshold
		}
		results, err := svc.Memory.Search(r.Context(), ports.SearchQuery{
			Query:   query,
			Project: r.URL.Query().Get("project"),
			Limit:   queryInt(r, "limit", baseLimit),
		})
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		threshold := queryFloat(r, "threshold", baseThreshold)
		exclude := parseIDSet(r.URL.Query().Get("exclude"))
		ids := service.RelevantIDs(results, threshold, exclude)
		// Telemetría de inyección: se anota aquí, donde el daemon ya sabe exactamente qué devolvió,
		// en vez de pedirle al hook que informe de vuelta (una llamada más por prompt que además
		// podría perderse). Best-effort a propósito: si la anotación falla, la inyección sigue.
		if svc.Usage != nil {
			if err := svc.Usage.RecordInjected(r.Context(), ids); err != nil {
				log.Printf("telemetría de inyección: %v", err)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"context": service.FormatRelevant(results, threshold, exclude),
			"ids":     ids,
		})
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

// endSessionRequest es el cuerpo OPCIONAL de POST /sessions/{id}/end. Sin cuerpo, la sesión se
// cierra igual: los dos campos solo alimentan la telemetría de uso.
type endSessionRequest struct {
	// TranscriptPath es la ruta del archivo con la conversación completa, que el agente le pasa al
	// hook de cierre. Es la única forma automática de saber si una memoria inyectada sirvió de algo.
	TranscriptPath string `json:"transcript_path"`
	// InjectedIDs son las memorias que se inyectaron durante la sesión. Las aporta el hook, que las
	// lleva registradas para no repetirlas; el daemon no las guarda por sesión.
	InjectedIDs []int64 `json:"injected_ids"`
}

// handleEndSession cierra una sesión (marca su instante de cierre) y, si el cuerpo trae la
// transcripción y los ids inyectados, mide cuántos de ellos acabaron apareciendo en las respuestas
// del agente. El cuerpo es opcional y su procesado es best-effort: cerrar la sesión es lo que no
// puede fallar; medir es un extra que nunca debe impedirlo.
func handleEndSession(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := svc.Session.End(r.Context(), id); err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}

		var body endSessionRequest
		_ = json.NewDecoder(r.Body).Decode(&body) // cuerpo ausente o malformado = sin telemetría

		out := map[string]any{"id": id, "ended": true}
		if svc.Usage != nil && len(body.InjectedIDs) > 0 {
			res, err := svc.Usage.RecordMentions(r.Context(), body.InjectedIDs, body.TranscriptPath)
			if err != nil {
				log.Printf("telemetría de menciones (sesión %s): %v", id, err)
			} else {
				out["scanned"], out["mentioned"] = res.Scanned, res.Mentioned
			}
		}
		writeJSON(w, http.StatusOK, out)
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
		// El prompt se guardó, pero sin su sesión: la referenciada no existía. Se registra porque es
		// una anomalía real —normalmente el hook SessionStart la crea antes de cualquier prompt— y
		// sin este aviso quedaría invisible, que es justo el defecto que la auditoría de durabilidad
		// vino a corregir (docs/auditoria-durabilidad.md §5, hallazgo 5).
		if res.SessionDropped {
			log.Printf("prompt guardado SIN sesión: la sesión %q no existe (id de prompt %d)",
				body.SessionID, res.Prompt.ID)
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":              res.Prompt.ID,
			"inserted":        res.Inserted,
			"session_dropped": res.SessionDropped,
		})
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
			if res.Skipped > 0 {
				log.Printf("backfill (disparado): %d memoria(s) saltada(s) por texto no vectorizable (ids: %v)", res.Skipped, res.SkippedIDs)
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
