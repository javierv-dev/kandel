package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// candidatePoolFactor amplía el número de vecinos pedidos al índice respecto al límite final,
// porque el propio elemento y los borrados se descartan después.
const candidatePoolFactor = 3

// Valores por defecto de la detección automática de conflictos (§20.E). El corte y el top-K se
// pueden afinar por entorno (main los lee y los inyecta como opciones).
const (
	// defaultConflictCutoff es la similitud de coseno mínima para que una candidata pase a la etapa
	// cara (juez LLM). Filtra el ruido: solo las semánticamente muy cercanas se juzgan.
	defaultConflictCutoff = 0.85
	// defaultConflictTopK es cuántas candidatas (las más cercanas que superan el corte) se juzgan
	// por memoria, como mucho.
	defaultConflictTopK = 3
	// detectBatch es el tamaño de lote del barrido de detección (reanudable, no carga todo en RAM).
	detectBatch = 100
)

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

	cutoff float64 // corte de similitud para pasar al juez LLM
	topK   int     // máximo de candidatas juzgadas por memoria
	// mu serializa el barrido de detección: el daemon (tras el backfill) y el hook pueden
	// coincidir; sin esto se juzgarían las mismas memorias dos veces.
	mu sync.Mutex
}

// ConflictOption configura un ConflictService (afinado de la detección) sin romper a quienes
// llaman al constructor sin opciones.
type ConflictOption func(*ConflictService)

// WithDetectionTuning fija el corte de similitud y el top-K de la detección automática. Valores no
// positivos se ignoran (se conserva el default), para que un entorno mal puesto no la desactive.
func WithDetectionTuning(cutoff float64, topK int) ConflictOption {
	return func(c *ConflictService) {
		if cutoff > 0 {
			c.cutoff = cutoff
		}
		if topK > 0 {
			c.topK = topK
		}
	}
}

// NewConflictService construye el servicio con sus dependencias inyectadas.
func NewConflictService(memories ports.MemoryRepo, vectors ports.VectorIndex, embedder ports.Embedder, relations ports.RelationRepo, runner ports.AgentRunner, uow ports.UnitOfWork, clock ports.Clock, opts ...ConflictOption) *ConflictService {
	c := &ConflictService{
		memories:  memories,
		vectors:   vectors,
		embedder:  embedder,
		relations: relations,
		runner:    runner,
		uow:       uow,
		clock:     clock,
		cutoff:    defaultConflictCutoff,
		topK:      defaultConflictTopK,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
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

// SweepDetection corre la detección automática de conflictos sobre las normativas aún sin verificar
// (conflict_checked = 0). Es el barrido: lo dispara el daemon justo después del backfill de vectores
// (y, por su medio, el hook UserPromptSubmit). Si ya hay uno en curso, NO espera ni encola otro
// (coalesce con TryLock). Devuelve cuántas normativas verificó. Si el embebedor o el juez fallan,
// aborta devolviendo el error: las que queden sin verificar se reintentan en el próximo barrido y el
// banner "normativas sin verificar" (bandeja Salud de memoria) las mantiene visibles.
func (c *ConflictService) SweepDetection(ctx context.Context) (int, error) {
	if !c.mu.TryLock() {
		return 0, nil
	}
	defer c.mu.Unlock()
	checked := 0
	for {
		batch, err := c.memories.UncheckedNormatives(ctx, detectBatch)
		if err != nil {
			return checked, err
		}
		if len(batch) == 0 {
			return checked, nil
		}
		for _, mem := range batch {
			if err := c.detectOne(ctx, mem); err != nil {
				return checked, err
			}
			checked++
		}
	}
}

// detectOne ejecuta la detección para una normativa: embebe su contenido (ancla del asunto), busca
// las vecinas más cercanas, y solo juzga (etapa cara) las que superan el corte de similitud, hasta
// top-K. Al terminar la marca como verificada. Un fallo del embebedor o del juez se propaga (la deja
// sin verificar para reintentar). El vector propio se recalcula aquí en vez de reusar el almacenado
// para no depender de que el backfill ya haya corrido.
func (c *ConflictService) detectOne(ctx context.Context, mem domain.Memory) error {
	vector, err := c.embedder.Embed(ctx, mem.Content)
	if err != nil {
		return err
	}
	neighbors, err := c.vectors.Nearest(ctx, vector, c.topK*candidatePoolFactor+1)
	if err != nil {
		return err
	}
	judged := 0
	for _, n := range neighbors {
		if n.MemoryID == mem.ID {
			continue // no es candidata de sí misma
		}
		if n.Score < c.cutoff {
			break // vecinos ordenados por cercanía: por debajo del corte, el resto también
		}
		cand, err := c.memories.Get(ctx, n.MemoryID)
		if err != nil {
			return err
		}
		if cand == nil || !comparable(mem, *cand) {
			continue
		}
		if err := c.judgePair(ctx, mem, *cand); err != nil {
			return err
		}
		judged++
		if judged >= c.topK {
			break
		}
	}
	return c.memories.SetConflictChecked(ctx, mem.ID)
}

// judgePair invoca al juez LLM sobre un par y, solo si el veredicto es de conflicto, persiste la
// relación con el sentido y el verbo que fija resolveDirection. Los veredictos no conflictivos
// (related/compatible/scoped/not_conflict) no se persisten: no alimentan ni el banner ni la cascada.
func (c *ConflictService) judgePair(ctx context.Context, newMem, cand domain.Memory) error {
	verdict, err := c.runner.Judge(ctx, buildComparePrompt(newMem, cand))
	if err != nil {
		return err
	}
	if verdict.Relation != domain.RelationConflictsWith && verdict.Relation != domain.RelationSupersedes {
		return nil
	}
	source, target, kind := resolveDirection(newMem, cand, verdict.Relation)
	resolved := domain.Verdict{Relation: kind, Reasoning: verdict.Reasoning, Confidence: verdict.Confidence}
	_, err = c.persistVerdict(ctx, source, target, resolved)
	return err
}

// resolveDirection decide el sentido y el verbo de la relación a persistir. El TIPO no da autoridad;
// solo el scope precede (§20.E):
//   - cross-scope (project vs global): la de proyecto reemplaza a la global (cascada project >
//     global). Verbo supersedes; source = la de proyecto, target = la global. Al ocultarse la global
//     en la recuperación (§20.D) el conflicto se autoresuelve, sin intervención humana.
//   - mismo scope + supersedes: la más reciente reemplaza a la más antigua (source = la más reciente).
//   - mismo scope + conflicts_with: contradicción real que decide un humano; se conserva
//     conflicts_with (source = la memoria recién verificada) para que el banner la muestre.
func resolveDirection(newMem, cand domain.Memory, relation domain.RelationType) (source, target domain.Memory, kind domain.RelationType) {
	if newMem.Scope != cand.Scope {
		proj, glob := newMem, cand
		if cand.Scope == domain.ScopeProject {
			proj, glob = cand, newMem
		}
		return proj, glob, domain.RelationSupersedes
	}
	if relation == domain.RelationSupersedes {
		if cand.CreatedAt.After(newMem.CreatedAt) {
			return cand, newMem, domain.RelationSupersedes
		}
		return newMem, cand, domain.RelationSupersedes
	}
	return newMem, cand, domain.RelationConflictsWith
}

// comparable indica si dos normativas son comparables: mismo proyecto, o alguna es global (una
// preferencia global aplica en todos los proyectos). Evita cruzar decisiones de proyectos distintos.
func comparable(a, b domain.Memory) bool {
	if a.Scope == domain.ScopeGlobal || b.Scope == domain.ScopeGlobal {
		return true
	}
	return a.Project == b.Project
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
