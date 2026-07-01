package domain

import (
	"strings"
	"time"
)

// MemoryType clasifica una memoria curada.
type MemoryType string

const (
	// TypeDecision: una decisión tomada (incluye restricciones y orientación del proyecto).
	TypeDecision MemoryType = "decision"
	// TypePreference: un gusto o convención del usuario (idioma, estilo). Suele ser global.
	TypePreference MemoryType = "preference"
	// TypeArchitecture: estructura del sistema y patrones/idiomas de código recurrentes.
	TypeArchitecture MemoryType = "architecture"
	// TypeBugfix: causa raíz de un bug resuelto.
	TypeBugfix MemoryType = "bugfix"
	// TypeDiscovery: un hecho del sistema/entorno, aprendizaje o configuración (catch-all).
	TypeDiscovery MemoryType = "discovery"
)

// Valid indica si el tipo es uno de los reconocidos.
func (t MemoryType) Valid() bool {
	switch t {
	case TypeDecision, TypePreference, TypeArchitecture, TypeBugfix, TypeDiscovery:
		return true
	default:
		return false
	}
}

// DefaultScopeForType devuelve el scope por defecto cuando el llamante no lo especifica: una
// preferencia es del usuario y vale en todos sus proyectos (global); el resto pertenece al
// proyecto actual (project). El agente puede sobrescribirlo según el contexto.
func DefaultScopeForType(t MemoryType) Scope {
	if t == TypePreference {
		return ScopeGlobal
	}
	return ScopeProject
}

// IsNormative indica si el tipo es normativo (decisión o preferencia): manda cómo debe
// comportarse el proyecto o el agente. Solo estas memorias entran a la bandeja "Siempre" y son
// las únicas sobre las que corre la detección automática de conflictos (el resto son factuales).
func (t MemoryType) IsNormative() bool {
	return t == TypeDecision || t == TypePreference
}

// Resolution marca si una memoria es un pendiente por resolver y en qué fase está. Es ESTADO,
// no tipo: cualquier tipo de memoria puede llevarlo. Vacío = memoria normal (no es un pendiente).
type Resolution string

const (
	// ResolutionNone es el valor por defecto: la memoria no es un pendiente.
	ResolutionNone Resolution = ""
	// ResolutionOpen marca un pendiente sin resolver (aparece en la bandeja "Pendientes").
	ResolutionOpen Resolution = "open"
	// ResolutionResolved marca un pendiente ya resuelto que se conserva como registro.
	ResolutionResolved Resolution = "resolved"
)

// Valid indica si la resolución es una de las reconocidas.
func (r Resolution) Valid() bool {
	switch r {
	case ResolutionNone, ResolutionOpen, ResolutionResolved:
		return true
	default:
		return false
	}
}

// Memory es la unidad central de conocimiento: una memoria curada que sobrevive entre
// sesiones y se recupera después.
type Memory struct {
	ID        int64
	SyncID    string // identidad estable entre máquinas (para sync)
	SessionID string
	Project   string
	Type      MemoryType
	Scope     Scope
	Title     string
	Content   string
	ToolName  string // herramienta que originó la memoria, si aplica

	NormalizedHash string // huella del contenido normalizado, para dedupe
	RevisionCount  int    // número de veces que se actualizó esta memoria
	DuplicateCount int    // número de llegadas idénticas

	Resolution Resolution // estado de pendiente; vacío = memoria normal

	// ConflictChecked indica que la detección automática de conflictos ya corrió sobre esta
	// memoria (solo aplica a las normativas). Nace en false; el barrido lo pone en true tras
	// juzgar sus candidatas. Alimenta el banner "normativas sin verificar" cuando sigue en false.
	ConflictChecked bool

	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastSeenAt time.Time
	DeletedAt  *time.Time // soft delete; nil = viva
}

// IsPending indica si la memoria es un pendiente sin resolver (resolution=open). Lo consume
// la bandeja "Pendientes" del contexto que se inyecta al agente.
func (o Memory) IsPending() bool {
	return o.Resolution == ResolutionOpen
}

// IsDeleted indica si la memoria fue borrada (soft delete).
func (o Memory) IsDeleted() bool {
	return o.DeletedAt != nil
}

// Validate comprueba las invariantes de la entidad. La estructura What/Why/Where/Learned
// del contenido es una convención que se cuida en la capa de servicio, no aquí: el dominio
// solo exige título y contenido no vacíos, y tipo, alcance y resolución válidos.
func (o Memory) Validate() error {
	if strings.TrimSpace(o.Title) == "" {
		return ErrEmptyTitle
	}
	if strings.TrimSpace(o.Content) == "" {
		return ErrEmptyContent
	}
	if !o.Type.Valid() {
		return ErrInvalidType
	}
	if !o.Scope.Valid() {
		return ErrInvalidScope
	}
	if !o.Resolution.Valid() {
		return ErrInvalidResolution
	}
	return nil
}
