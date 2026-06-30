package domain

import (
	"strings"
	"time"
)

// MemoryType clasifica una memoria curada.
type MemoryType string

const (
	TypeDecision     MemoryType = "decision"
	TypeArchitecture MemoryType = "architecture"
	TypeBugfix       MemoryType = "bugfix"
	TypePattern      MemoryType = "pattern"
	TypeConfig       MemoryType = "config"
	TypeDiscovery    MemoryType = "discovery"
	TypeLearning     MemoryType = "learning"
)

// Valid indica si el tipo es uno de los reconocidos.
func (t MemoryType) Valid() bool {
	switch t {
	case TypeDecision, TypeArchitecture, TypeBugfix,
		TypePattern, TypeConfig, TypeDiscovery, TypeLearning:
		return true
	default:
		return false
	}
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
