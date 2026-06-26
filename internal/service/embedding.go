package service

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// defaultBackfillBatch es el tamaño de lote del backfill: se piden los pendientes de a poco
// para no cargar toda la base en memoria y para que la operación sea reanudable.
const defaultBackfillBatch = 100

// EmbeddingService cubre el mantenimiento de los vectores: rellenar los que faltan y reindexar
// los que quedaron obsoletos (p. ej. tras cambiar de modelo). El guardado normal ya genera el
// vector en MemoryService.Save; este servicio existe para los datos previos o para forzar un
// recálculo, expuesto por los comandos "embeddings backfill/reembed".
type EmbeddingService struct {
	memories ports.MemoryRepo
	embedder ports.Embedder
	vectors  ports.VectorIndex
	batch    int
}

// NewEmbeddingService construye el servicio con sus dependencias inyectadas.
func NewEmbeddingService(memories ports.MemoryRepo, embedder ports.Embedder, vectors ports.VectorIndex) *EmbeddingService {
	return &EmbeddingService{
		memories: memories,
		embedder: embedder,
		vectors:  vectors,
		batch:    defaultBackfillBatch,
	}
}

// BackfillResult resume lo que hizo Backfill.
type BackfillResult struct {
	Embedded int // número de memorias a las que se les calculó (o recalculó) el vector
}

// Backfill calcula el vector de toda memoria viva que no lo tenga, o que lo tenga de un modelo
// distinto al activo. Procesa por lotes y es idempotente y reanudable: cada vector que se
// escribe sale del conjunto de pendientes, así que reejecutar tras un fallo continúa donde se
// quedó. Si el embebedor falla, aborta devolviendo cuántos llevaba (parcial, reanudable).
func (s *EmbeddingService) Backfill(ctx context.Context) (BackfillResult, error) {
	model := s.embedder.Model()
	var result BackfillResult
	for {
		ids, err := s.vectors.PendingEmbedding(ctx, model, s.batch)
		if err != nil {
			return result, err
		}
		if len(ids) == 0 {
			return result, nil
		}
		progressed := false
		for _, id := range ids {
			done, err := s.reembed(ctx, id, model)
			if err != nil {
				return result, err
			}
			if done {
				result.Embedded++
				progressed = true
			}
		}
		// Salvaguarda: si un lote no escribió ningún vector (p. ej. todas las filas
		// desaparecieron en una carrera), no hay forma de avanzar; cortar evita un bucle infinito.
		if !progressed {
			return result, nil
		}
	}
}

// Reembed fuerza el recálculo del vector de una sola memoria por id. Devuelve domain.ErrNotFound
// si la memoria no existe (o está borrada).
func (s *EmbeddingService) Reembed(ctx context.Context, id int64) error {
	done, err := s.reembed(ctx, id, s.embedder.Model())
	if err != nil {
		return err
	}
	if !done {
		return domain.ErrNotFound
	}
	return nil
}

// reembed carga la memoria, calcula su vector y lo guarda. Devuelve false (sin error) si la
// memoria ya no existe, para que el backfill la salte sin abortar.
func (s *EmbeddingService) reembed(ctx context.Context, id int64, model string) (bool, error) {
	mem, err := s.memories.Get(ctx, id)
	if err != nil {
		return false, err
	}
	if mem == nil {
		return false, nil
	}
	vector, err := s.embedder.Embed(ctx, mem.Content)
	if err != nil {
		return false, err
	}
	if err := s.vectors.Upsert(ctx, id, vector, model); err != nil {
		return false, err
	}
	return true, nil
}
