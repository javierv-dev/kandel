package ports

import (
	"context"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
)

// SyncRepo gobierna la cola de mutaciones, el cursor del change-feed y los leases. Está aislado del
// resto de repos a propósito: solo lo toca service/sync. La regla de oro es que encolar una mutación
// ocurre en la MISMA transacción que el cambio que la origina (la transacción viaja en el context),
// para que nunca exista un dato sin su mutación.
type SyncRepo interface {
	// EnqueueMutation añade una mutación a la cola. Si el context lleva una transacción abierta
	// por la unidad de trabajo, se encola dentro de ella (atómico con el cambio que la origina).
	EnqueueMutation(ctx context.Context, m domain.SyncMutation) error
	// ListPending devuelve las mutaciones aún sin confirmar para un destino, en orden de Seq,
	// a partir de afterSeq (exclusivo).
	ListPending(ctx context.Context, target string, afterSeq int64, limit int) ([]domain.SyncMutation, error)
	// Ack confirma como replicadas las mutaciones con los Seq indicados.
	Ack(ctx context.Context, target string, seqs []int64) error

	// GetCursor devuelve el cursor del destino: el mayor server_seq del hub ya aplicado localmente.
	// Un destino sin cursor devuelve 0 (arranque en frío: el Pull traerá el estado actual completo).
	GetCursor(ctx context.Context, target string) (int64, error)
	// SetCursor guarda el cursor del destino tras aplicar un Pull.
	SetCursor(ctx context.Context, target string, cursor int64) error

	// AcquireLease intenta tomar el lease de sincronización de un destino; false si está tomado.
	AcquireLease(ctx context.Context, target, owner string, ttl time.Duration) (bool, error)
	// ReleaseLease libera el lease de un destino si lo posee owner.
	ReleaseLease(ctx context.Context, target, owner string) error
}
