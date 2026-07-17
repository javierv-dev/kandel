package service

import (
	"context"
	"errors"
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
	memories     ports.MemoryRepo
	embedder     ports.Embedder
	vectors      ports.VectorIndex
	titleVectors ports.VectorIndex // índice del vector del título; nil = no se rellenan títulos
	batch        int
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

// WithTitleBackfill activa el relleno del vector del TÍTULO junto al del contenido. El backfill se
// dirige por los pendientes del índice de contenido (título y contenido se escriben siempre juntos,
// así que comparten estado): al recalcular una memoria se embeben ambos en una sola llamada. Un
// índice nil se ignora.
func WithTitleBackfill(titleVectors ports.VectorIndex) EmbeddingOption {
	return func(s *EmbeddingService) {
		if titleVectors != nil {
			s.titleVectors = titleVectors
		}
	}
}

// BackfillResult resume lo que hizo Backfill.
type BackfillResult struct {
	Embedded   int     // número de memorias a las que se les calculó (o recalculó) el vector
	Skipped    int     // memorias saltadas porque el modelo no pudo vectorizar su texto (ErrUnembeddable)
	SkippedIDs []int64 // ids de las memorias saltadas, para poder reportarlas y reintentarlas luego
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
// Corre en dos frentes sobre la misma fila de vectores: primero el CONTENIDO (memorias sin fila o de
// otro modelo; reembed llena de paso el título cuando el índice de títulos está activo), y luego el
// TÍTULO (memorias con contenido al día pero sin vector de título: el hueco típico tras la migración).
func (s *EmbeddingService) backfill(ctx context.Context) (BackfillResult, error) {
	model := s.embedder.Model()
	var result BackfillResult
	// skipped acumula las memorias que el modelo no pudo vectorizar, compartido por los dos frentes:
	// evita reintentar (y recontar) la misma memoria envenenada, que PendingEmbedding vuelve a listar
	// en cada lote mientras siga sin vector.
	skipped := map[int64]bool{}
	if err := s.drainPending(ctx, s.vectors, model, skipped, func(id int64) (bool, error) {
		return s.reembed(ctx, id, model)
	}, &result); err != nil {
		return result, err
	}
	if s.titleVectors != nil {
		if err := s.drainPending(ctx, s.titleVectors, model, skipped, func(id int64) (bool, error) {
			return s.reembedTitle(ctx, id, model)
		}, &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

// drainPending vacía una cola de pendientes por lotes reanudables: pide un lote a idx.PendingEmbedding,
// lo rellena con fill y repite hasta que no queden. Corta si un lote no progresa (p. ej. las filas
// desaparecieron en una carrera, o solo quedan memorias no vectorizables), para evitar un bucle
// infinito. Comparte la mecánica de los dos frentes. El mapa skipped es compartido entre frentes.
func (s *EmbeddingService) drainPending(ctx context.Context, idx ports.VectorIndex, model string, skipped map[int64]bool, fill func(id int64) (bool, error), result *BackfillResult) error {
	for {
		ids, err := idx.PendingEmbedding(ctx, model, s.batch)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		progressed := false
		for _, id := range ids {
			if skipped[id] {
				continue // ya marcada como no vectorizable: no reintentar el embed ni recontarla
			}
			done, err := fill(id)
			if err != nil {
				// Un texto que el modelo no puede vectorizar (bge-m3 → NaN) NO aborta la pasada: se
				// marca la memoria y se sigue con las demás. Cualquier otro error (embebedor caído,
				// red, DB) SÍ aborta: barrer toda la base contra un backend roto no tiene sentido.
				if errors.Is(err, ports.ErrUnembeddable) {
					skipped[id] = true
					result.Skipped++
					result.SkippedIDs = append(result.SkippedIDs, id)
					continue
				}
				return err
			}
			if done {
				result.Embedded++
				progressed = true
			}
		}
		if !progressed {
			return nil
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
	// Con el índice de títulos activo, embebe contenido y título en una sola llamada y guarda ambos;
	// sin él, solo el contenido (conducta previa).
	if s.titleVectors != nil {
		vectors, err := s.embedder.EmbedBatch(ctx, []string{mem.Content, mem.Title})
		if err != nil {
			return false, err
		}
		if err := s.vectors.Upsert(ctx, id, vectors[0], model); err != nil {
			return false, err
		}
		if err := s.titleVectors.Upsert(ctx, id, vectors[1], model); err != nil {
			return false, err
		}
		return true, nil
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

// reembedTitle calcula y guarda solo el vector del TÍTULO de una memoria (su fila de contenido ya
// existe). Es el frente de backfill que rellena el título faltante tras la migración. Devuelve false
// (sin error) si la memoria ya no existe, para que el barrido la salte sin abortar.
func (s *EmbeddingService) reembedTitle(ctx context.Context, id int64, model string) (bool, error) {
	mem, err := s.memories.Get(ctx, id)
	if err != nil {
		return false, err
	}
	if mem == nil {
		return false, nil
	}
	vector, err := s.embedder.Embed(ctx, mem.Title)
	if err != nil {
		return false, err
	}
	if err := s.titleVectors.Upsert(ctx, id, vector, model); err != nil {
		return false, err
	}
	return true, nil
}
