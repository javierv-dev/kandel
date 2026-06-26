package domain

import (
	"strings"
	"time"
)

// MemoryType clasifica una memoria curada. El tipo influye en el decaimiento
// (cada cuánto la memoria pide revisión).
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

// LifecycleState es el estado de ciclo de vida derivado (no se almacena: se calcula al leer).
type LifecycleState string

const (
	// StateActive indica una memoria vigente.
	StateActive LifecycleState = "active"
	// StateNeedsReview indica una memoria cuya fecha de revisión ya venció.
	StateNeedsReview LifecycleState = "needs_review"
)

// reviewMonthsByType define el decaimiento por tipo: meses que se suman a la fecha de
// creación para fijar review_after. Los tipos ausentes no decaen (review_after queda en
// su valor cero).
//
// Nota: el proyecto viejo definía el decaimiento sobre tipos "decision/policy/preference",
// pero solo "decision" coincide con los tipos reales. Aquí se porta ese único caso fiable;
// el resto de la política de decaimiento queda pendiente de confirmar (ver BITACORA).
var reviewMonthsByType = map[MemoryType]int{
	TypeDecision: 6,
}

// ReviewAfterFor calcula la fecha de revisión para un tipo dado a partir de un instante.
// Devuelve el tiempo cero si el tipo no decae.
func ReviewAfterFor(t MemoryType, from time.Time) time.Time {
	months, ok := reviewMonthsByType[t]
	if !ok {
		return time.Time{}
	}
	return from.AddDate(0, months, 0)
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

	TopicKey       TopicKey // tema al que pertenece; vacío = ninguno
	NormalizedHash string   // huella del contenido normalizado, para dedupe
	RevisionCount  int      // número de revisiones del mismo tema
	DuplicateCount int      // número de llegadas idénticas

	Pinned bool // fijada arriba en el contexto; local, NO se sincroniza

	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastSeenAt  time.Time
	ReviewAfter time.Time  // tras esta fecha la memoria pide revisión; cero = no decae
	DeletedAt   *time.Time // soft delete; nil = viva
}

// State devuelve el estado de ciclo de vida virtual respecto a un instante de referencia.
// Sin fecha de revisión, o mientras la fecha no haya quedado en el pasado, la memoria está
// activa. Solo pide revisión cuando review_after ya pasó (estrictamente antes de "now"),
// coherente con su nombre: la revisión llega *tras* esa fecha.
func (o Memory) State(now time.Time) LifecycleState {
	if o.ReviewAfter.IsZero() || !o.ReviewAfter.Before(now) {
		return StateActive
	}
	return StateNeedsReview
}

// IsDeleted indica si la memoria fue borrada (soft delete).
func (o Memory) IsDeleted() bool {
	return o.DeletedAt != nil
}

// Validate comprueba las invariantes de la entidad. La estructura What/Why/Where/Learned
// del contenido es una convención que se cuida en la capa de servicio, no aquí: el dominio
// solo exige título y contenido no vacíos, tipo y alcance válidos, y topic_key bien formado
// cuando está presente.
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
	if !o.TopicKey.IsZero() {
		if _, err := ParseTopicKey(string(o.TopicKey)); err != nil {
			return err
		}
	}
	return nil
}
