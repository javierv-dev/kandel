package domain

import "time"

// SyncEntity identifica qué clase de entidad describe una mutación de sync.
type SyncEntity string

const (
	SyncEntitySession  SyncEntity = "session"
	SyncEntityMemory   SyncEntity = "memory"
	SyncEntityPrompt   SyncEntity = "prompt"
	SyncEntityRelation SyncEntity = "relation"
)

// Valid indica si la entidad de sync es una de las reconocidas.
func (e SyncEntity) Valid() bool {
	switch e {
	case SyncEntitySession, SyncEntityMemory, SyncEntityPrompt, SyncEntityRelation:
		return true
	default:
		return false
	}
}

// SyncOp es la operación que registra una mutación: alta/actualización o borrado.
type SyncOp string

const (
	SyncOpUpsert SyncOp = "upsert"
	SyncOpDelete SyncOp = "delete"
)

// Valid indica si la operación de sync es una de las reconocidas.
func (o SyncOp) Valid() bool {
	switch o {
	case SyncOpUpsert, SyncOpDelete:
		return true
	default:
		return false
	}
}

// SyncMutation es una entrada de la cola de sincronización: el registro inmutable de un
// cambio local listo para replicarse. La cola es la fuente de verdad de qué falta enviar a
// cada destino (TargetKey); el orden lo fija Seq.
type SyncMutation struct {
	Seq        int64
	TargetKey  string     // destino al que pertenece esta entrada de cola
	Entity     SyncEntity // qué tipo de entidad cambió
	EntityKey  string     // identidad estable de la entidad (sync_id)
	Op         SyncOp     // upsert o delete
	Payload    []byte     // JSON canónico del estado de la entidad
	Source     string     // máquina/origen que generó el cambio
	Project    string     // proyecto al que pertenece (para filtrar por enrolamiento)
	OccurredAt time.Time
	AckedAt    *time.Time // nil mientras no se confirme la replicación
}
