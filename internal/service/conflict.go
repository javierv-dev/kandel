package service

import (
	"context"
	"fmt"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// candidatePoolFactor amplía el número de vecinos pedidos al índice respecto al límite final,
// porque el propio elemento y los borrados se descartan después.
const candidatePoolFactor = 3

// ConflictService detecta y juzga conflictos entre memorias. La detección de candidatos es el
// extremo BARATO del embudo (vecinos por coseno); el juicio LLM es el extremo CARO, aplicado solo
// a los candidatos. Con el núcleo vectorial el punto ciego léxico del viejo desaparece: se
// encuentran candidatos por significado aunque no compartan palabras.
type ConflictService struct {
	memories  ports.MemoryRepo
	vectors   ports.VectorIndex
	embedder  ports.Embedder
	relations ports.RelationRepo
	runner    ports.AgentRunner
	uow       ports.UnitOfWork
	clock     ports.Clock
}

// NewConflictService construye el servicio con sus dependencias inyectadas.
func NewConflictService(memories ports.MemoryRepo, vectors ports.VectorIndex, embedder ports.Embedder, relations ports.RelationRepo, runner ports.AgentRunner, uow ports.UnitOfWork, clock ports.Clock) *ConflictService {
	return &ConflictService{
		memories:  memories,
		vectors:   vectors,
		embedder:  embedder,
		relations: relations,
		runner:    runner,
		uow:       uow,
		clock:     clock,
	}
}

// Candidate es una memoria candidata a entrar en conflicto con otra, con su cercanía semántica.
type Candidate struct {
	Memory domain.Memory
	Score  float64
}

// FindCandidates devuelve las memorias semánticamente más cercanas a la dada (posibles
// conflictos), excluyéndola a ella misma. Es read-only y barato: NO llama al LLM. Devuelve
// domain.ErrNotFound si la memoria no existe.
func (c *ConflictService) FindCandidates(ctx context.Context, memoryID int64, limit int) ([]Candidate, error) {
	memory, err := c.memories.Get(ctx, memoryID)
	if err != nil {
		return nil, err
	}
	if memory == nil {
		return nil, domain.ErrNotFound
	}
	vector, err := c.embedder.Embed(ctx, memory.Content)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 5
	}
	// Se pide un pool ampliado porque la propia memoria y las borradas se descartan después.
	neighbors, err := c.vectors.Nearest(ctx, vector, limit*candidatePoolFactor+1)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, limit)
	for _, n := range neighbors {
		if n.MemoryID == memoryID {
			continue // no es candidato de sí misma
		}
		cand, err := c.memories.Get(ctx, n.MemoryID)
		if err != nil {
			return nil, err
		}
		if cand == nil {
			continue // borrada o inexistente
		}
		out = append(out, Candidate{Memory: *cand, Score: n.Score})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Compare ejecuta el juicio LLM entre dos memorias y persiste el veredicto como una relación
// juzgada. Es el extremo caro: invoca al AgentRunner. Devuelve domain.ErrNotFound si alguna de
// las memorias no existe.
func (c *ConflictService) Compare(ctx context.Context, sourceID, targetID int64) (domain.MemoryRelation, error) {
	source, target, err := c.loadPair(ctx, sourceID, targetID)
	if err != nil {
		return domain.MemoryRelation{}, err
	}
	verdict, err := c.runner.Judge(ctx, buildComparePrompt(*source, *target))
	if err != nil {
		return domain.MemoryRelation{}, err
	}
	return c.persistVerdict(ctx, *source, *target, verdict)
}

// RecordVerdict persiste un veredicto YA emitido por el agente (sin volver a llamar al LLM): es el
// camino de mem_judge, donde el agente que llama ya decidió la relación. Devuelve
// domain.ErrNotFound si alguna memoria no existe; valida el verbo de relación.
func (c *ConflictService) RecordVerdict(ctx context.Context, sourceID, targetID int64, verdict domain.Verdict) (domain.MemoryRelation, error) {
	if !verdict.Relation.Valid() || verdict.Relation == domain.RelationPending {
		return domain.MemoryRelation{}, fmt.Errorf("%w: relación %q", domain.ErrInvalidType, verdict.Relation)
	}
	source, target, err := c.loadPair(ctx, sourceID, targetID)
	if err != nil {
		return domain.MemoryRelation{}, err
	}
	return c.persistVerdict(ctx, *source, *target, verdict)
}

// loadPair carga las dos memorias por id, devolviendo ErrNotFound si falta alguna.
func (c *ConflictService) loadPair(ctx context.Context, sourceID, targetID int64) (*domain.Memory, *domain.Memory, error) {
	source, err := c.memories.Get(ctx, sourceID)
	if err != nil {
		return nil, nil, err
	}
	target, err := c.memories.Get(ctx, targetID)
	if err != nil {
		return nil, nil, err
	}
	if source == nil || target == nil {
		return nil, nil, domain.ErrNotFound
	}
	return source, target, nil
}

// persistVerdict inserta la relación juzgada entre dos memorias (referenciadas por su sync_id) y
// la devuelve. La inserción va dentro de una transacción.
func (c *ConflictService) persistVerdict(ctx context.Context, source, target domain.Memory, verdict domain.Verdict) (domain.MemoryRelation, error) {
	now := c.clock.Now().UTC()
	relation := domain.MemoryRelation{
		SyncID:     newSyncID("rel"),
		SourceID:   source.SyncID,
		TargetID:   target.SyncID,
		Kind:       verdict.Relation,
		Reason:     verdict.Reasoning,
		Confidence: verdict.Confidence,
		Status:     domain.JudgmentJudged,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	err := c.uow.Do(ctx, func(ctx context.Context) error {
		id, err := c.relations.Insert(ctx, relation)
		if err != nil {
			return err
		}
		relation.ID = id
		return nil
	})
	if err != nil {
		return domain.MemoryRelation{}, err
	}
	return relation, nil
}

// comparePromptTemplate es el prompt canónico de comparación. Es deliberadamente estable: cambiarlo
// cambia el significado de los veredictos guardados y rompe la comparación entre modelos.
const comparePromptTemplate = `Eres un auditor de memoria semántica. Compara las dos memorias y clasifica su relación.

=== Memoria A ===
ID: %[1]s
Título: %[2]s
Contenido: %[3]s

=== Memoria B ===
ID: %[4]s
Título: %[5]s
Contenido: %[6]s

Elige EXACTAMENTE UNA relación de este vocabulario cerrado:
- conflicts_with   — afirman cosas contradictorias
- supersedes       — una reemplaza o anula a la otra
- scoped           — una es un caso más específico de la otra
- related           — comparten tema pero no se contradicen
- compatible       — son consistentes y se complementan
- not_conflict     — no se relacionan; sin solape semántico relevante

Responde con un único objeto JSON en una línea y nada más:
{"relation":"<verbo>","confidence":<0.0-1.0>,"reasoning":"<≤200 caracteres>"}`

// buildComparePrompt arma el prompt de comparación para un par de memorias.
func buildComparePrompt(a, b domain.Memory) string {
	return fmt.Sprintf(comparePromptTemplate,
		a.SyncID, a.Title, a.Content,
		b.SyncID, b.Title, b.Content,
	)
}
