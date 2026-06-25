package ports

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
)

// RecentQuery filtra una lectura directa de observaciones recientes. Es el camino de
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
	Type    domain.ObservationType // vacío = cualquier tipo
	Project string
	Scope   domain.Scope // vacío = sin filtrar por alcance
	Limit   int
}

// ObservationRepo persiste y recupera observaciones. Interfaz cohesiva (no un God Object):
// solo lo que toca a la entidad observación. La búsqueda semántica vive en VectorIndex; aquí
// Search recibe ya los vecinos resueltos y los hidrata a entidades completas.
type ObservationRepo interface {
	// Add inserta una observación nueva y devuelve su id local.
	Add(ctx context.Context, o domain.Observation) (int64, error)
	// Get recupera una observación por su id local; nil si no existe.
	Get(ctx context.Context, id int64) (*domain.Observation, error)
	// GetBySyncID recupera por identidad estable de sync.
	GetBySyncID(ctx context.Context, syncID string) (*domain.Observation, error)
	// Update reemplaza los campos editables de una observación y devuelve el estado final.
	Update(ctx context.Context, id int64, o domain.Observation) (*domain.Observation, error)
	// SoftDelete marca la observación como borrada sin quitarla (DeletedAt).
	SoftDelete(ctx context.Context, id int64) error
	// HardDelete elimina la observación y sus datos asociados (incluido su vector).
	HardDelete(ctx context.Context, id int64) error
	// Recent lista observaciones por recencia para navegación del usuario.
	Recent(ctx context.Context, q RecentQuery) ([]domain.Observation, error)
	// Search hidrata a SearchResult los vecinos devueltos por el VectorIndex, aplicando los
	// filtros de q (tipo/proyecto/alcance) y respetando el orden de cercanía.
	Search(ctx context.Context, q SearchQuery, neighbors []Neighbor) ([]domain.SearchResult, error)
	// FindByHash busca una observación viva con el mismo hash normalizado (dedupe exacto).
	FindByHash(ctx context.Context, project string, scope domain.Scope, hash string) (*domain.Observation, error)
	// LatestByTopicKey devuelve la observación viva más reciente de un mismo tema.
	LatestByTopicKey(ctx context.Context, project string, scope domain.Scope, key domain.TopicKey) (*domain.Observation, error)
	// SetPinned fija o desfija una observación (estado local, no se sincroniza).
	SetPinned(ctx context.Context, id int64, pinned bool) error
	// NeedingReview lista observaciones cuya fecha de revisión ya venció.
	NeedingReview(ctx context.Context, project string, limit int) ([]domain.Observation, error)
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
}

// RelationRepo persiste y recupera relaciones/juicios entre memorias (detección de conflictos).
type RelationRepo interface {
	// Insert crea una relación (típicamente pending) y devuelve su id local.
	Insert(ctx context.Context, r domain.Relation) (int64, error)
	// Get recupera una relación por id; nil si no existe.
	Get(ctx context.Context, id int64) (*domain.Relation, error)
	// Judge aplica el veredicto del LLM a una relación pendiente.
	Judge(ctx context.Context, id int64, v domain.Verdict) error
	// ListByStatus lista relaciones por estado de juicio.
	ListByStatus(ctx context.Context, status domain.JudgmentStatus, limit int) ([]domain.Relation, error)
}
