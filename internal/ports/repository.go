package ports

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
)

// RecentQuery filtra una lectura directa de memorias recientes. Es el camino de
// recuperación del usuario (navegación): no depende del embedder.
type RecentQuery struct {
	Project string
	Scope   domain.Scope // vacío = sin filtrar por alcance
	Limit   int
}

// SearchQuery describe una búsqueda semántica. La consulta en texto se convierte en vector
// y se rankea por cercanía; los filtros acotan el universo antes de rankear.
type SearchQuery struct {
	Query   string
	Type    domain.MemoryType // vacío = cualquier tipo
	Project string
	Scope   domain.Scope // vacío = sin filtrar por alcance
	Limit   int
}

// MemoryRepo persiste y recupera memorias. Interfaz cohesiva (no un God Object):
// solo lo que toca a la entidad memoria. La búsqueda semántica vive en VectorIndex; aquí
// Search recibe ya los vecinos resueltos y los hidrata a entidades completas.
type MemoryRepo interface {
	// Add inserta una memoria nueva y devuelve su id local.
	Add(ctx context.Context, o domain.Memory) (int64, error)
	// Get recupera una memoria por su id local; nil si no existe.
	Get(ctx context.Context, id int64) (*domain.Memory, error)
	// GetBySyncID recupera por identidad estable de sync.
	GetBySyncID(ctx context.Context, syncID string) (*domain.Memory, error)
	// Update reemplaza los campos editables de una memoria y devuelve el estado final.
	Update(ctx context.Context, id int64, o domain.Memory) (*domain.Memory, error)
	// SoftDelete marca la memoria como borrada sin quitarla (DeletedAt).
	SoftDelete(ctx context.Context, id int64) error
	// Restore revierte un soft-delete (limpia DeletedAt). Solo afecta a memorias borradas
	// lógicamente; si la memoria no existe o no estaba borrada, devuelve domain.ErrNotFound.
	Restore(ctx context.Context, id int64) error
	// HardDelete elimina la memoria y sus datos asociados (incluido su vector).
	HardDelete(ctx context.Context, id int64) error
	// Recent lista memorias por recencia para navegación del usuario.
	Recent(ctx context.Context, q RecentQuery) ([]domain.Memory, error)
	// Search hidrata a SearchResult los vecinos devueltos por el VectorIndex, aplicando los
	// filtros de q (tipo/proyecto/alcance) y respetando el orden de cercanía.
	Search(ctx context.Context, q SearchQuery, neighbors []Neighbor) ([]domain.SearchResult, error)
	// FindByHash busca una memoria viva con el mismo hash normalizado (dedupe exacto).
	FindByHash(ctx context.Context, project string, scope domain.Scope, hash string) (*domain.Memory, error)
	// NeedingReview lista memorias cuya fecha de revisión ya venció.
	NeedingReview(ctx context.Context, project string, limit int) ([]domain.Memory, error)
	// UpdateProject renombra el proyecto de todas las memorias que lo tengan; devuelve cuántas
	// filas cambió. Lo usa la migración de proyecto (renombrado por cambio de remoto git).
	UpdateProject(ctx context.Context, oldProject, newProject string) (int64, error)
}

// SessionRepo persiste y recupera sesiones.
type SessionRepo interface {
	// Create abre una sesión nueva.
	Create(ctx context.Context, s domain.Session) error
	// End cierra una sesión y le adjunta un resumen.
	End(ctx context.Context, id, summary string) error
	// Get recupera una sesión por id; nil si no existe.
	Get(ctx context.Context, id string) (*domain.Session, error)
	// Recent lista las sesiones más recientes de un proyecto.
	Recent(ctx context.Context, project string, limit int) ([]domain.Session, error)
	// MostRecentActive devuelve el id de la sesión abierta más reciente del proyecto.
	// El segundo valor es false si no hay ninguna abierta.
	MostRecentActive(ctx context.Context, project string) (string, bool, error)
	// UpdateProject renombra el proyecto de todas las sesiones que lo tengan; devuelve cuántas
	// filas cambió.
	UpdateProject(ctx context.Context, oldProject, newProject string) (int64, error)
}

// PromptRepo persiste y recupera prompts (contexto crudo del usuario).
type PromptRepo interface {
	// Add guarda un prompt y devuelve su id local.
	Add(ctx context.Context, p domain.Prompt) (int64, error)
	// AddIfMissing guarda el prompt solo si no existe uno equivalente; el bool indica si se
	// insertó (true) o ya existía (false).
	AddIfMissing(ctx context.Context, p domain.Prompt) (int64, bool, error)
	// Recent lista los prompts más recientes de un proyecto.
	Recent(ctx context.Context, project string, limit int) ([]domain.Prompt, error)
	// Delete elimina un prompt por id.
	Delete(ctx context.Context, id int64) error
	// UpdateProject renombra el proyecto de todos los prompts que lo tengan; devuelve cuántas
	// filas cambió.
	UpdateProject(ctx context.Context, oldProject, newProject string) (int64, error)
}

// RelationRepo persiste y recupera relaciones/juicios entre memorias (detección de conflictos).
type RelationRepo interface {
	// Insert crea una relación (típicamente pending) y devuelve su id local.
	Insert(ctx context.Context, r domain.MemoryRelation) (int64, error)
	// Get recupera una relación por id; nil si no existe.
	Get(ctx context.Context, id int64) (*domain.MemoryRelation, error)
	// Judge aplica el veredicto del LLM a una relación pendiente.
	Judge(ctx context.Context, id int64, v domain.Verdict) error
	// ListByStatus lista relaciones por estado de juicio.
	ListByStatus(ctx context.Context, status domain.JudgmentStatus, limit int) ([]domain.MemoryRelation, error)
}
