package service

import (
	"context"
	"sync"

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
	// mu serializa los backfills: el barrido del daemon, los disparos por hook y el comando manual
	// pueden coincidir; sin esto correrían a la vez y reembeberían las mismas memorias dos veces.
	mu sync.Mutex
	// postBackfill se invoca tras un backfill exitoso. Es el enganche flojo con la detección de
	// conflictos (§20.F): una vez que los vectores diferidos están al día, corre el barrido de
	// detección sobre las normativas sin verificar. Opcional (nil = no hacer nada); no fusiona
	// servicios (main lo cablea a ConflictService.SweepDetection).
	postBackfill func(ctx context.Context)
}

// NewEmbeddingService construye el servicio con sus dependencias inyectadas.
func NewEmbeddingService(memories ports.MemoryRepo, embedder ports.Embedder, vectors ports.VectorIndex, opts ...EmbeddingOption) *EmbeddingService {
	s := &EmbeddingService{
		memories: memories,
		embedder: embedder,
		vectors:  vectors,
		batch:    defaultBackfillBatch,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// EmbeddingOption configura un EmbeddingService opcionalmente.
type EmbeddingOption func(*EmbeddingService)

// WithPostBackfill inyecta el enganche que corre tras un backfill exitoso (la detección de
// conflictos diferida). Un hook nil se ignora.
func WithPostBackfill(hook func(ctx context.Context)) EmbeddingOption {
	return func(s *EmbeddingService) {
		if hook != nil {
			s.postBackfill = hook
		}
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
// Es el camino MANUAL (`kandel embeddings backfill`): bloquea hasta tener el lock (espera si hay
// otro backfill en curso) y corre completo.
func (s *EmbeddingService) Backfill(ctx context.Context) (BackfillResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.backfill(ctx)
	s.runPostBackfill(ctx, err)
	return r, err
}

// SweepBackfill es la variante para los disparadores AUTOMÁTICOS (barrido periódico del daemon y
// hook UserPromptSubmit). Si ya hay un backfill en curso, NO espera ni encola otro: lo omite y
// devuelve skipped=true. Así varios disparos solapados coalescen en una sola pasada.
func (s *EmbeddingService) SweepBackfill(ctx context.Context) (BackfillResult, bool, error) {
	if !s.mu.TryLock() {
		return BackfillResult{}, true, nil
	}
	defer s.mu.Unlock()
	r, err := s.backfill(ctx)
	s.runPostBackfill(ctx, err)
	return r, false, err
}

// runPostBackfill dispara el enganche posterior al backfill (detección de conflictos diferida) solo
// si el backfill no falló: si el embebedor está caído, la detección tampoco podría correr. Es
// best-effort y no altera el resultado del backfill.
func (s *EmbeddingService) runPostBackfill(ctx context.Context, backfillErr error) {
	if backfillErr != nil || s.postBackfill == nil {
		return
	}
	s.postBackfill(ctx)
}

// Warmup fuerza una petición de embeddings de sondeo ("ping") para que el modelo quede cargado en
// memoria. Lo dispara el hook SessionStart al inicio de la conversación, de modo que el primer
// Save real no pague el arranque en frío (~7s) de Ollama. Devuelve el error del embebedor si lo hay.
func (s *EmbeddingService) Warmup(ctx context.Context) error {
	_, err := s.embedder.Embed(ctx, "ping")
	return err
}

// backfill es la implementación sin candado; lo toman Backfill (bloqueante) y SweepBackfill (TryLock).
func (s *EmbeddingService) backfill(ctx context.Context) (BackfillResult, error) {
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
