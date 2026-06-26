package domain

import "time"

// RelationType clasifica el tipo de relación entre dos memorias. Nace como pending
// (placeholder, sin clasificar) y, al juzgarse, pasa a uno de los veredictos.
type RelationType string

const (
	// RelationPending es la ausencia de veredicto: hay un candidato, nadie lo ha juzgado.
	RelationPending RelationType = "pending"
	RelationRelated RelationType = "related"
	// RelationCompatible indica que conviven sin contradecirse.
	RelationCompatible RelationType = "compatible"
	// RelationScoped indica que aplican a ámbitos distintos.
	RelationScoped RelationType = "scoped"
	// RelationConflictsWith indica afirmaciones contradictorias.
	RelationConflictsWith RelationType = "conflicts_with"
	// RelationSupersedes indica que una reemplaza/anula a la otra.
	RelationSupersedes RelationType = "supersedes"
	// RelationNotConflict indica que no hay solape semántico relevante.
	RelationNotConflict RelationType = "not_conflict"
)

// Valid indica si el tipo de relación es uno de los reconocidos.
func (k RelationType) Valid() bool {
	switch k {
	case RelationPending, RelationRelated, RelationCompatible, RelationScoped,
		RelationConflictsWith, RelationSupersedes, RelationNotConflict:
		return true
	default:
		return false
	}
}

// JudgmentStatus indica si una relación ya fue juzgada.
type JudgmentStatus string

const (
	// JudgmentPending indica que aún no hay veredicto.
	JudgmentPending JudgmentStatus = "pending"
	// JudgmentJudged indica que ya hay veredicto.
	JudgmentJudged JudgmentStatus = "judged"
	// JudgmentOrphaned indica que una de las memorias relacionadas fue borrada.
	JudgmentOrphaned JudgmentStatus = "orphaned"
	// JudgmentIgnored indica que la relación se descartó.
	JudgmentIgnored JudgmentStatus = "ignored"
)

// Valid indica si el estado de juicio es uno de los reconocidos.
func (s JudgmentStatus) Valid() bool {
	switch s {
	case JudgmentPending, JudgmentJudged, JudgmentOrphaned, JudgmentIgnored:
		return true
	default:
		return false
	}
}

// MemoryRelation es una relación o juicio entre dos memorias, usada para detectar conflictos.
// SourceID y TargetID referencian a las memorias por su identidad estable de sync (sync_id).
type MemoryRelation struct {
	ID         int64
	SyncID     string
	SourceID   string // sync_id de la memoria A
	TargetID   string // sync_id de la memoria B
	Kind       RelationType
	Reason     string
	Evidence   string
	Confidence float64
	Status     JudgmentStatus
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// IsConflict indica si la relación, ya juzgada, representa una contradicción.
func (r MemoryRelation) IsConflict() bool {
	return r.Status == JudgmentJudged && r.Kind == RelationConflictsWith
}
