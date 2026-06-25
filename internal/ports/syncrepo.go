package ports

import (
	"context"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
)

// SyncRepo gobierna la cola de mutaciones, el estado de chunks sincronizados y los leases.
// Está aislado del resto de repos a propósito: solo lo toca service/sync. La regla de oro es
// que encolar una mutación ocurre en la MISMA transacción que el cambio que la origina
// (de ahí el parámetro Tx en EnqueueMutation), para que nunca exista un dato sin su mutación.
type SyncRepo interface {
	// EnqueueMutation añade una mutación a la cola dentro de la transacción dada.
	EnqueueMutation(ctx context.Context, tx Tx, m domain.SyncMutation) error
	// ListPending devuelve las mutaciones aún sin confirmar para un destino, en orden de Seq,
	// a partir de afterSeq (exclusivo).
	ListPending(ctx context.Context, target string, afterSeq int64, limit int) ([]domain.SyncMutation, error)
	// Ack confirma como replicadas las mutaciones con los Seq indicados.
	Ack(ctx context.Context, target string, seqs []int64) error

	// RecordChunk marca un chunk como ya sincronizado con un destino (idempotencia de envío).
	RecordChunk(ctx context.Context, target, chunkID string) error
	// SyncedChunks devuelve el conjunto de chunks ya sincronizados con un destino.
	SyncedChunks(ctx context.Context, target string) (map[string]bool, error)

	// AcquireLease intenta tomar el lease de sincronización de un destino; false si está tomado.
	AcquireLease(ctx context.Context, target, owner string, ttl time.Duration) (bool, error)
	// ReleaseLease libera el lease de un destino si lo posee owner.
	ReleaseLease(ctx context.Context, target, owner string) error
}
