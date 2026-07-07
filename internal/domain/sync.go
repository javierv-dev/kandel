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

// SyncConflict registra una edición concurrente detectada al aplicar un Pull: llegó un cambio remoto
// para una memoria que localmente tenía una mutación pendiente (sin subir). No se aplica a ciegas —no
// se pisa el disco local— y queda aquí hasta que el usuario decide (conservar la local o tomar la
// remota). Es la señal barata de §6.5 del plan de sync: pendiente local + cambio remoto en la misma
// key = edición concurrente. Consumidor principal: la bitácora abierta (type=journal), un documento
// vivo que se edita a mano.
type SyncConflict struct {
	SyncID     string    // identidad de la memoria en conflicto
	Project    string    // proyecto de la fila entrante (para filtrar el aviso por proyecto)
	IncomingOp SyncOp    // operación remota (upsert o delete)
	Incoming   []byte    // payload remoto: para "tomar la remota" o mostrar el diff
	IncomingAt time.Time // updated_at remoto
	DetectedAt time.Time // cuándo se detectó el conflicto
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
