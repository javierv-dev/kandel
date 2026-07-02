package mcp

import (
	"context"

	"github.com/javierv-dev/kandel/internal/diagnostic"
	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/service"
	"github.com/mark3labs/mcp-go/server"
)

// MemoryPort es la cara de los casos de uso de memoria que el MCP consume. Se declara como
// interfaz (no el *service.MemoryService concreto) para poder inyectar dobles en los tests.
type MemoryPort interface {
	Save(ctx context.Context, in service.SaveInput) (service.SaveResult, error)
	Get(ctx context.Context, id int64) (*domain.Memory, error)
	Recent(ctx context.Context, q ports.RecentQuery) ([]domain.Memory, error)
	Search(ctx context.Context, q ports.SearchQuery) ([]domain.SearchResult, error)
	Update(ctx context.Context, id int64, in service.UpdateInput) (domain.Memory, error)
	Delete(ctx context.Context, id int64) error
	Restore(ctx context.Context, id int64) (domain.Memory, error)
}

// SessionPort es la cara de los casos de uso de sesión que el MCP consume.
type SessionPort interface {
	Start(ctx context.Context, in service.StartInput) (domain.Session, error)
	End(ctx context.Context, id, summary string) error
	Get(ctx context.Context, id string) (*domain.Session, error)
	MostRecentActive(ctx context.Context, project string) (string, bool, error)
}

// LifecyclePort es la cara de los casos de uso de ciclo de vida que el MCP consume.
type LifecyclePort interface {
	PendingResolution(ctx context.Context, project string, limit int) ([]domain.Memory, error)
}

// PromptPort es la cara de los casos de uso de prompts que el MCP consume.
type PromptPort interface {
	Save(ctx context.Context, in service.SavePromptInput) (service.SavePromptResult, error)
}

// DoctorPort corre los chequeos de salud de solo lectura. La diagnostic.Runner la cumple.
type DoctorPort interface {
	Run(ctx context.Context) diagnostic.Report
}

// ConflictPort es la cara de la detección y juicio de conflictos que el MCP consume.
type ConflictPort interface {
	Compare(ctx context.Context, sourceID, targetID int64) (domain.MemoryRelation, error)
	RecordVerdict(ctx context.Context, sourceID, targetID int64, verdict domain.Verdict) (domain.MemoryRelation, error)
}

// Services agrupa los casos de uso que necesitan los handlers MCP. Es el equivalente al App de
// la CLI: el wiring concreto vive en cmd/kandel/main.go.
type Services struct {
	Memory    MemoryPort
	Session   SessionPort
	Lifecycle LifecyclePort
	Prompt    PromptPort
	Doctor    DoctorPort
	Conflict  ConflictPort

	// DefaultProject es un override de proyecto a nivel de proceso (p. ej. KANDEL_PROJECT o
	// `kandel mcp --project NAME`), usado cuando una llamada no trae proyecto explícito.
	DefaultProject string
}

// serverInstructions le dice al cliente MCP cuándo usar las herramientas de kandel. La búsqueda
// es 100% semántica (no hay búsqueda léxica): el agente busca por significado.
const serverInstructions = `kandel ofrece memoria persistente que sobrevive entre sesiones.

HERRAMIENTAS PRINCIPALES:
  mem_save — guarda decisiones, bugs, descubrimientos y convenciones DE FORMA PROACTIVA (no esperes a que te lo pidan)
  mem_search — busca por significado trabajo, decisiones o contexto de sesiones anteriores (búsqueda semántica)
  mem_context — recupera memorias recientes (al inicio de sesión o tras una compactación)
  mem_get — obtiene el contenido completo de una memoria por su id
  mem_session_summary — guarda el resumen de fin de sesión

REGLA DE GUARDADO PROACTIVO: llama a mem_save justo después de CUALQUIER decisión, arreglo de bug,
descubrimiento o convención — no solo cuando te lo pidan.`

// NewServer construye el servidor MCP registrando solo las herramientas permitidas por la
// allowlist (nil = todas). Devuelve un *server.MCPServer listo para servir por stdio.
func NewServer(svc Services, allowlist map[string]bool) *server.MCPServer {
	srv := server.NewMCPServer(
		"kandel",
		"0.1.0",
		server.WithToolCapabilities(true),
		server.WithInstructions(serverInstructions),
		// Registra cada llamada a tool en ~/.kandel/logs/mcp.log (observabilidad del camino MCP).
		server.WithToolHandlerMiddleware(loggingMiddleware),
	)
	registerTools(srv, svc, allowlist)
	return srv
}

// registerTools añade al servidor cada herramienta cuyo nombre pase la allowlist. Cada grupo
// vive en su archivo (tools_memory.go, tools_session.go, tools_lifecycle.go).
func registerTools(srv *server.MCPServer, svc Services, allowlist map[string]bool) {
	registerMemoryTools(srv, svc, allowlist)
	registerSessionTools(srv, svc, allowlist)
	registerLifecycleTools(srv, svc, allowlist)
	registerContextTools(srv, svc, allowlist)
	registerDoctorTools(srv, svc, allowlist)
	registerConflictTools(srv, svc, allowlist)
}
