// Package http es el adaptador de entrada de la API HTTP local: traduce peticiones REST a los
// casos de uso (service/) usando solo la biblioteca estándar (enrutado por método+patrón de
// Go 1.22+), sin framework. Espejo de las mismas operaciones que la CLI y el MCP.
package http

import (
	"context"
	"net/http"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/service"
)

// MemoryPort es la cara de los casos de uso de memoria que la API consume.
type MemoryPort interface {
	Save(ctx context.Context, in service.SaveInput) (service.SaveResult, error)
	Get(ctx context.Context, id int64) (*domain.Memory, error)
	Recent(ctx context.Context, q ports.RecentQuery) ([]domain.Memory, error)
	Search(ctx context.Context, q ports.SearchQuery) ([]domain.SearchResult, error)
	Update(ctx context.Context, id int64, in service.UpdateInput) (domain.Memory, error)
	Delete(ctx context.Context, id int64, hard bool) error
}

// SessionPort es la cara de los casos de uso de sesión que la API consume.
type SessionPort interface {
	Start(ctx context.Context, in service.StartInput) (domain.Session, error)
	End(ctx context.Context, id, summary string) error
	Get(ctx context.Context, id string) (*domain.Session, error)
}

// LifecyclePort es la cara de los casos de uso de ciclo de vida que la API consume.
type LifecyclePort interface {
	Pin(ctx context.Context, id int64) error
	Unpin(ctx context.Context, id int64) error
	NeedingReview(ctx context.Context, project string, limit int) ([]domain.Memory, error)
}

// PromptPort es la cara del guardado de prompts que la API consume.
type PromptPort interface {
	Save(ctx context.Context, in service.SavePromptInput) (service.SavePromptResult, error)
}

// ContextPort arma el bloque de contexto que los hooks inyectan al agente al iniciar sesión.
type ContextPort interface {
	Format(ctx context.Context, project string, scope domain.Scope) (string, error)
}

// ProjectPort migra (renombra) un proyecto en todas sus entidades.
type ProjectPort interface {
	Migrate(ctx context.Context, oldProject, newProject string) (service.MigrateResult, error)
}

// EmbeddingPort cubre el mantenimiento de vectores que los hooks disparan: backfill de las memorias
// que se guardaron sin vector (Ollama caído) y warm-up del modelo al iniciar sesión. Ambos se
// ejecutan de forma asíncrona en el daemon; los endpoints solo los lanzan y responden 202.
type EmbeddingPort interface {
	SweepBackfill(ctx context.Context) (service.BackfillResult, bool, error)
	Warmup(ctx context.Context) error
}

// Services agrupa los casos de uso que necesita la API. El wiring concreto vive en
// cmd/kandel/main.go; aquí solo se ven las interfaces.
type Services struct {
	Memory    MemoryPort
	Session   SessionPort
	Lifecycle LifecyclePort
	Prompt    PromptPort
	Context   ContextPort
	Project   ProjectPort
	Embedding EmbeddingPort
}

// NewHandler construye el enrutador con todas las rutas registradas. Devuelve un http.Handler
// listo para servir (testeable con httptest sin abrir un puerto).
func NewHandler(svc Services) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", handleHealth)

	mux.HandleFunc("POST /memories", handleSaveMemory(svc))
	mux.HandleFunc("GET /memories/recent", handleRecent(svc))
	mux.HandleFunc("GET /memories/{id}", handleGetMemory(svc))
	mux.HandleFunc("PATCH /memories/{id}", handleUpdateMemory(svc))
	mux.HandleFunc("DELETE /memories/{id}", handleDeleteMemory(svc))
	mux.HandleFunc("POST /memories/{id}/pin", handlePin(svc, true))
	mux.HandleFunc("POST /memories/{id}/unpin", handlePin(svc, false))

	mux.HandleFunc("GET /search", handleSearch(svc))
	mux.HandleFunc("GET /review", handleReview(svc))

	mux.HandleFunc("POST /sessions", handleStartSession(svc))
	mux.HandleFunc("POST /sessions/{id}/end", handleEndSession(svc))
	mux.HandleFunc("GET /sessions/{id}", handleGetSession(svc))

	mux.HandleFunc("POST /prompts", handleSavePrompt(svc))

	// Endpoints que consumen los hooks de integración con agentes (ver §18 del plan).
	mux.HandleFunc("GET /context", handleContext(svc))
	mux.HandleFunc("POST /memories/passive", handlePassiveCapture(svc))
	mux.HandleFunc("POST /projects/migrate", handleMigrateProject(svc))

	// Mantenimiento de vectores disparado por hooks (asíncrono; respuesta 202).
	mux.HandleFunc("POST /embeddings/backfill", handleBackfill(svc))
	mux.HandleFunc("POST /embeddings/warmup", handleWarmup(svc))

	return mux
}

// handleHealth responde un OK simple para sondas de vida.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
