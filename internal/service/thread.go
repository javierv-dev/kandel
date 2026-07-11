package service

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// maxThreadDepth acota cuántos eslabones recorre Thread hacia atrás. Es una salvaguarda contra
// cadenas patológicas o ciclos (que además detecta el conjunto `seen`); en la práctica un hilo tiene
// pocas fases.
const maxThreadDepth = 100

// ThreadService implementa la trazabilidad de la bitácora (Q3 del rediseño de contexto): enlaza
// entradas journal del mismo hilo con la relación `continues` y recorre esa cadena para reconstruir
// "cómo llegamos a este estado". Los enlaces los crea el agente en la transición de fase; la lectura
// la usa la tool mem_thread y la miga del contexto inyectado.
type ThreadService struct {
	memories  ports.MemoryRepo
	relations ports.RelationRepo
	uow       ports.UnitOfWork
	clock     ports.Clock
}

// NewThreadService construye el servicio con sus dependencias inyectadas.
func NewThreadService(memories ports.MemoryRepo, relations ports.RelationRepo, uow ports.UnitOfWork, clock ports.Clock) *ThreadService {
	return &ThreadService{memories: memories, relations: relations, uow: uow, clock: clock}
}

// Link crea el enlace `continues` de una entrada de bitácora hacia su predecesora del mismo hilo:
// newerID (la fase nueva) continúa a olderID (la fase anterior). Es una aserción del agente, así que
// nace ya juzgada. Idempotente: si ese enlace ya existe, lo devuelve sin duplicar. Devuelve
// domain.ErrNotFound si falta alguna de las dos memorias, y domain.ErrValidation si son la misma.
func (s *ThreadService) Link(ctx context.Context, newerID, olderID int64) (domain.MemoryRelation, error) {
	if newerID == olderID {
		return domain.MemoryRelation{}, domain.ErrSelfContinue
	}
	newer, err := s.memories.Get(ctx, newerID)
	if err != nil {
		return domain.MemoryRelation{}, err
	}
	older, err := s.memories.Get(ctx, olderID)
	if err != nil {
		return domain.MemoryRelation{}, err
	}
	if newer == nil || older == nil {
		return domain.MemoryRelation{}, domain.ErrNotFound
	}

	// Idempotencia: si la fase nueva ya continúa a esta misma predecesora, no dupliques.
	if existing, err := s.relations.FindBySourceKind(ctx, newer.SyncID, domain.RelationContinues); err != nil {
		return domain.MemoryRelation{}, err
	} else if existing != nil && existing.TargetID == older.SyncID {
		return *existing, nil
	}

	now := s.clock.Now().UTC()
	rel := domain.MemoryRelation{
		SyncID:     newSyncID("rel"),
		SourceID:   newer.SyncID,
		TargetID:   older.SyncID,
		Kind:       domain.RelationContinues,
		Confidence: 1,
		Status:     domain.JudgmentJudged,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	err = s.uow.Do(ctx, func(ctx context.Context) error {
		id, err := s.relations.Insert(ctx, rel)
		if err != nil {
			return err
		}
		rel.ID = id
		return nil
	})
	if err != nil {
		return domain.MemoryRelation{}, err
	}
	return rel, nil
}

// Thread reconstruye el hilo de bitácora que termina en id, recorriendo la cadena `continues` hacia
// atrás (source → target). Devuelve las entradas ordenadas de la MÁS ANTIGUA a la más nueva (el orden
// en que ocurrieron), con id incluido al frente. Corta ante un ciclo, una predecesora borrada o el
// tope de profundidad. Devuelve domain.ErrNotFound si id no existe.
func (s *ThreadService) Thread(ctx context.Context, id int64) ([]domain.Memory, error) {
	cur, err := s.memories.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, domain.ErrNotFound
	}

	// Se construye de la más nueva a la más antigua y luego se invierte.
	chain := []domain.Memory{*cur}
	seen := map[string]bool{cur.SyncID: true}
	for i := 0; i < maxThreadDepth; i++ {
		rel, err := s.relations.FindBySourceKind(ctx, cur.SyncID, domain.RelationContinues)
		if err != nil {
			return nil, err
		}
		if rel == nil || rel.TargetID == "" || seen[rel.TargetID] {
			break
		}
		pred, err := s.memories.GetBySyncID(ctx, rel.TargetID)
		if err != nil {
			return nil, err
		}
		if pred == nil {
			break // predecesora borrada: el hilo se corta aquí
		}
		chain = append(chain, *pred)
		seen[pred.SyncID] = true
		cur = pred
	}

	// Invierte a orden cronológico (antigua → nueva).
	for l, r := 0, len(chain)-1; l < r; l, r = l+1, r-1 {
		chain[l], chain[r] = chain[r], chain[l]
	}
	return chain, nil
}
