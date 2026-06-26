package http

import (
	"net/http"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/service"
)

// saveMemoryRequest es el cuerpo de POST /memories.
type saveMemoryRequest struct {
	SessionID string `json:"session_id"`
	Project   string `json:"project"`
	Type      string `json:"type"`
	Scope     string `json:"scope"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	ToolName  string `json:"tool_name"`
	TopicKey  string `json:"topic_key"`
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
		res, err := svc.Memory.Save(r.Context(), service.SaveInput{
			SessionID: body.SessionID,
			Project:   body.Project,
			Type:      domain.MemoryType(body.Type),
			Scope:     scope,
			Title:     body.Title,
			Content:   body.Content,
			ToolName:  body.ToolName,
			TopicKey:  body.TopicKey,
		})
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":      res.Memory.ID,
			"outcome": string(res.Outcome),
			"memory":  res.Memory,
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
	Type     *string `json:"type"`
	Title    *string `json:"title"`
	Content  *string `json:"content"`
	Scope    *string `json:"scope"`
	TopicKey *string `json:"topic_key"`
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
		if body.TopicKey != nil {
			in.TopicKey = body.TopicKey
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
		m, err := svc.Memory.Update(r.Context(), id, in)
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, m)
	}
}

// handleDeleteMemory borra una memoria (soft por defecto, ?hard=true para borrado físico).
func handleDeleteMemory(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		hard := r.URL.Query().Get("hard") == "true"
		if err := svc.Memory.Delete(r.Context(), id, hard); err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handlePin fija o desfija una memoria.
func handlePin(svc Services, pinned bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var err error
		if pinned {
			err = svc.Lifecycle.Pin(r.Context(), id)
		} else {
			err = svc.Lifecycle.Unpin(r.Context(), id)
		}
		if err != nil {
			writeError(w, statusForError(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "pinned": pinned})
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

// handleReview lista las memorias cuya fecha de revisión venció (decaimiento).
func handleReview(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		memories, err := svc.Lifecycle.NeedingReview(r.Context(), r.URL.Query().Get("project"), queryInt(r, "limit", 20))
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

// endSessionRequest es el cuerpo de POST /sessions/{id}/end.
type endSessionRequest struct {
	Summary string `json:"summary"`
}

// handleEndSession cierra una sesión con un resumen.
func handleEndSession(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var body endSessionRequest
		if r.ContentLength != 0 && !decodeBody(w, r, &body) {
			return
		}
		if err := svc.Session.End(r.Context(), id, body.Summary); err != nil {
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
