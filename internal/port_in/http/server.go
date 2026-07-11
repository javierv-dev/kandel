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
	Delete(ctx context.Context, id int64) error
}

// SessionPort es la cara de los casos de uso de sesión que la API consume.
type SessionPort interface {
	Start(ctx context.Context, in service.StartInput) (domain.Session, error)
	End(ctx context.Context, id string) error
}

// LifecyclePort es la cara de los casos de uso de ciclo de vida que la API consume.
type LifecyclePort interface {
	PendingResolution(ctx context.Context, project string, limit int) ([]domain.Memory, error)
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

// SyncPort cubre los disparos ASÍNCRONOS de sincronización que los hooks lanzan contra el daemon:
// push al cerrar la sesión y pull al iniciarla. Ambos corren en segundo plano (los endpoints solo los
// lanzan y responden 202), serializados y omitibles si ya hay un auto-sync en curso (bool skipped).
type SyncPort interface {
	AutoPush(ctx context.Context) (service.PushResult, bool, error)
	AutoPull(ctx context.Context) (service.PullResult, bool, error)
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
	Sync      SyncPort

	// RelevanceThreshold y RelevanceLimit fijan la línea base de la inyección de memoria por
	// relevancia (endpoint /relevant). Un valor no positivo usa el default del handler. El parámetro
	// de consulta (threshold/limit) sigue teniendo prioridad sobre esta línea base.
	RelevanceThreshold float64
	RelevanceLimit     int

	// OnShutdown, si no es nil, habilita el endpoint POST /shutdown que apaga el daemon de forma
	// ordenada (lo usa `kandel stop`). El daemon corre la imagen del propio binario y lo mantiene
	// bloqueado en Windows; pararlo libera el .exe para poder actualizarlo. El wiring vive en
	// serve.go; en los tests y en usos sin daemon se deja nil y la ruta NO se registra (así no se
	// expone el apagado por accidente).
	OnShutdown func()
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

	mux.HandleFunc("GET /search", handleSearch(svc))
	mux.HandleFunc("GET /relevant", handleRelevant(svc))
	mux.HandleFunc("GET /pending", handlePending(svc))

	mux.HandleFunc("POST /sessions", handleStartSession(svc))
	mux.HandleFunc("POST /sessions/{id}/end", handleEndSession(svc))

	mux.HandleFunc("POST /prompts", handleSavePrompt(svc))

	// Endpoints que consumen los hooks de integración con agentes (ver §18 del plan).
	mux.HandleFunc("GET /context", handleContext(svc))
	mux.HandleFunc("POST /projects/migrate", handleMigrateProject(svc))

	// Mantenimiento de vectores disparado por hooks (asíncrono; respuesta 202).
	mux.HandleFunc("POST /embeddings/backfill", handleBackfill(svc))
	mux.HandleFunc("POST /embeddings/warmup", handleWarmup(svc))

	// Sincronización disparada por hooks (asíncrono; respuesta 202). El Stop hook pega en /sync/push;
	// el SessionStart en /sync/pull. El daemon los corre de fondo sin bloquear la terminal.
	mux.HandleFunc("POST /sync/push", handleSyncPush(svc))
	mux.HandleFunc("POST /sync/pull", handleSyncPull(svc))

	// Apagado ordenado (lo pide `kandel stop`). Solo se registra si el daemon cableó el disparador,
	// así el endpoint no existe en los tests ni en usos sin daemon.
	if svc.OnShutdown != nil {
		mux.HandleFunc("POST /shutdown", handleShutdown(svc.OnShutdown))
	}

	return mux
}

// handleHealth responde un OK simple para sondas de vida.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleShutdown responde 202 y dispara el apagado ordenado del daemon en segundo plano. El
// disparo va en una goroutine para que la respuesta llegue al cliente antes de que el servidor
// deje de aceptar conexiones (http.Server.Shutdown espera a que esta misma petición termine).
func handleShutdown(trigger func()) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "apagando"})
		go trigger()
	}
}
