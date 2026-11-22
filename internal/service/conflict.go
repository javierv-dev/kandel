package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/project"
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
	// defaultFactualCutoff es el corte de la pasada de supersede factual (§20.E-bis). Más exigente que
	// el normativo: los factuales son muchos y de alta rotación, así que un umbral más alto acota el
	// gasto del juez LLM y evita emparejar hechos que solo comparten tema.
	defaultFactualCutoff = 0.90
	// defaultConflictTopK es cuántas candidatas (las más cercanas que superan el corte) se juzgan
	// por memoria, como mucho.
	defaultConflictTopK = 3
	// detectBatch es el tamaño de lote del barrido de detección (reanudable, no carga todo en RAM).
	detectBatch = 100
	// defaultTitleCutoff es el corte del portón por TÍTULO (§20.E-ter). Un título muy parecido es raro
	// entre memorias distintas y común entre casi-duplicados, así que es un disparador de alta
	// precisión: surface candidatas que el coseno de contenido (más bajo en paráfrasis) pierde, sin
	// bajar el corte de contenido ni inundar al juez.
	defaultTitleCutoff = 0.88
	// duplicateConfidenceThreshold es la confianza mínima del juez para AUTO-OCULTAR un casi-duplicado.
	// Por debajo, el par se deja para revisión humana (bandeja Salud de memoria, Capa 3) en vez de
	// ocultar algo por una corazonada del modelo. Fijado en 0.85 a partir de datos reales: un duplicado
	// obvio (memorias 1↔47, "Arquitectura hexagonal") lo juzga el modelo con 0.87, así que 0.90 dejaba
	// fuera duplicados legítimos.
	duplicateConfidenceThreshold = 0.85
)

// ConflictService detecta y juzga conflictos entre memorias. La detección de candidatos es el
// extremo BARATO del embudo (vecinos por coseno); el juicio LLM es el extremo CARO, aplicado solo
// a los candidatos. Con el núcleo vectorial el punto ciego léxico del viejo desaparece: se
// encuentran candidatos por significado aunque no compartan palabras.
type ConflictService struct {
	memories     ports.MemoryRepo
	vectors      ports.VectorIndex
	titleVectors ports.VectorIndex // índice del vector del título para el portón por título; nil = deshabilitado
	embedder     ports.Embedder
	relations    ports.RelationRepo
	runner       ports.AgentRunner
	uow          ports.UnitOfWork
	clock        ports.Clock

	cutoff        float64 // corte de similitud para pasar al juez LLM (pasada normativa)
	factualCutoff float64 // corte de la pasada de supersede factual (§20.E-bis), más alto
	titleCutoff   float64 // corte del portón por título (§20.E-ter)
	topK          int     // máximo de candidatas juzgadas por memoria
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

// WithFactualTuning fija el corte de la pasada de supersede factual (§20.E-bis). Un valor no positivo
// se ignora (se conserva el default), para que un entorno mal puesto no la desactive.
func WithFactualTuning(cutoff float64) ConflictOption {
	return func(c *ConflictService) {
		if cutoff > 0 {
			c.factualCutoff = cutoff
		}
	}
}

// WithTitleGate activa el portón por título de la detección de casi-duplicados (§20.E-ter): además
// de las candidatas por coseno de contenido, se buscan las de título muy parecido (índice paralelo).
// Un índice nil se ignora. titleCutoff no positivo conserva el default.
func WithTitleGate(titleVectors ports.VectorIndex, titleCutoff float64) ConflictOption {
	return func(c *ConflictService) {
		if titleVectors != nil {
			c.titleVectors = titleVectors
		}
		if titleCutoff > 0 {
			c.titleCutoff = titleCutoff
		}
	}
}

// NewConflictService construye el servicio con sus dependencias inyectadas.
func NewConflictService(memories ports.MemoryRepo, vectors ports.VectorIndex, embedder ports.Embedder, relations ports.RelationRepo, runner ports.AgentRunner, uow ports.UnitOfWork, clock ports.Clock, opts ...ConflictOption) *ConflictService {
	c := &ConflictService{
		memories:      memories,
		vectors:       vectors,
		embedder:      embedder,
		relations:     relations,
		runner:        runner,
		uow:           uow,
		clock:         clock,
		cutoff:        defaultConflictCutoff,
		factualCutoff: defaultFactualCutoff,
		titleCutoff:   defaultTitleCutoff,
		topK:          defaultConflictTopK,
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

// Compare ejecuta el juicio LLM entre dos memorias y, si hay conflicto, persiste la relación
// resuelta. Es el extremo caro: invoca al AgentRunner. Comparte con el barrido la misma lógica de
// resolución (resolveAndPersist): aplica resolveDirection —así un cruce project/global también se
// resuelve como supersedes y dispara la cascada— y NO persiste los veredictos no conflictivos
// (related/compatible/scoped/not_conflict), que devuelve como relación efímera sin fila. Devuelve
// domain.ErrNotFound si alguna de las memorias no existe.
func (c *ConflictService) Compare(ctx context.Context, sourceID, targetID int64) (domain.MemoryRelation, error) {
	source, target, err := c.loadPair(ctx, sourceID, targetID)
	if err != nil {
		return domain.MemoryRelation{}, err
	}
	verdict, err := c.runner.Judge(ctx, buildComparePrompt(*source, *target))
	if err != nil {
		return domain.MemoryRelation{}, err
	}
	return c.resolveAndPersist(ctx, *source, *target, verdict)
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

// SweepDetection corre la detección automática de conflictos sobre las memorias aún sin verificar
// (conflict_checked = 0) en dos pasadas: primero las normativas (embudo completo: supersede o
// conflicts_with para humano) y luego los factuales (§20.E-bis: solo supersede). Es el barrido: lo
// dispara el daemon justo después del backfill de vectores (y, por su medio, el hook UserPromptSubmit).
// Si ya hay uno en curso, NO espera ni encola otro (coalesce con TryLock). Devuelve cuántas memorias
// verificó (ambas pasadas). Si el embebedor o el juez fallan, aborta devolviendo el error: las que
// queden sin verificar se reintentan en el próximo barrido y el banner "normativas sin verificar"
// (bandeja Salud de memoria) las mantiene visibles.
func (c *ConflictService) SweepDetection(ctx context.Context) (int, error) {
	if !c.mu.TryLock() {
		return 0, nil
	}
	defer c.mu.Unlock()
	checked := 0
	// Pasada normativa: embudo completo (supersede + conflicts_with para humano).
	n, err := c.sweepQueue(ctx, c.memories.UncheckedNormatives, c.detectOne)
	checked += n
	if err != nil {
		return checked, err
	}
	// Pasada factual (§20.E-bis): solo detecta reemplazos (supersede), con corte más alto.
	n, err = c.sweepQueue(ctx, c.memories.UncheckedFactuals, c.detectFactualOne)
	checked += n
	return checked, err
}

// sweepQueue drena una cola de detección por lotes reanudables: pide un lote a next, procesa cada
// memoria con detect y repite hasta vaciarla. Devuelve cuántas procesó y aborta al primer error (las
// que queden se reintentan en el próximo barrido). Comparte la mecánica de las dos pasadas de
// SweepDetection; lo único que cambia es la cola de origen y la función de detección.
func (c *ConflictService) sweepQueue(
	ctx context.Context,
	next func(context.Context, int) ([]domain.Memory, error),
	detect func(context.Context, domain.Memory) error,
) (int, error) {
	checked := 0
	for {
		batch, err := next(ctx, detectBatch)
		if err != nil {
			return checked, err
		}
		if len(batch) == 0 {
			return checked, nil
		}
		for _, mem := range batch {
			if err := detect(ctx, mem); err != nil {
				return checked, err
			}
			checked++
		}
	}
}

// detectOne ejecuta la detección normativa para una memoria: embudo completo (juez LLM que puede
// producir supersede o conflicts_with).
func (c *ConflictService) detectOne(ctx context.Context, mem domain.Memory) error {
	return c.detect(ctx, mem, c.cutoff, c.judgePair)
}

// detectFactualOne ejecuta la pasada de supersede factual (§20.E-bis) para una memoria: mismo embudo,
// pero con el corte más alto y un juez que SOLO persiste reemplazos (supersede), descartando el resto.
func (c *ConflictService) detectFactualOne(ctx context.Context, mem domain.Memory) error {
	return c.detect(ctx, mem, c.factualCutoff, c.judgeFactualPair)
}

// detect es la mecánica compartida por las dos pasadas: embebe el contenido de mem (ancla del asunto),
// busca las vecinas más cercanas, y solo juzga (etapa cara) las que superan el corte, hasta top-K. Al
// terminar la marca como verificada. Un fallo del embebedor o del juez se propaga (la deja sin
// verificar para reintentar). El vector propio se recalcula aquí en vez de reusar el almacenado para no
// depender de que el backfill ya haya corrido. cutoff y judge son lo único que cambia entre pasadas.
func (c *ConflictService) detect(ctx context.Context, mem domain.Memory, cutoff float64, judge func(context.Context, domain.Memory, domain.Memory) error) error {
	candidateIDs, err := c.candidateIDs(ctx, mem, cutoff)
	if err != nil {
		return err
	}
	judged := 0
	for _, id := range candidateIDs {
		cand, err := c.memories.Get(ctx, id)
		if err != nil {
			return err
		}
		if cand == nil || !comparable(mem, *cand) {
			continue
		}
		// Si el par ya tiene veredicto juzgado (lo hizo el otro lado del par, un barrido anterior o
		// mem_compare), no se vuelve a invocar al juez LLM: la comprobación es barata y evita el juicio
		// redundante que antes producía filas simétricas (detectOne(A) y detectOne(B)). No consume el
		// presupuesto top-K porque no cuesta.
		existing, err := c.relations.FindJudgedPair(ctx, mem.SyncID, cand.SyncID)
		if err != nil {
			return err
		}
		if existing != nil {
			continue
		}
		if err := judge(ctx, mem, *cand); err != nil {
			return err
		}
		judged++
		if judged >= c.topK {
			break
		}
	}
	return c.memories.SetConflictChecked(ctx, mem.ID)
}

// candidateIDs reúne los ids de las candidatas a juzgar de mem: las cercanas por coseno de CONTENIDO
// (≥ cutoff) y, si el índice de títulos está activo, también las de TÍTULO muy parecido (≥ titleCutoff,
// §20.E-ter). Une ambas fuentes preservando el orden (primero por contenido, luego las que solo aporta
// el título), sin la propia memoria ni repetidas. El vector propio se recalcula aquí (contenido y título
// en una sola llamada) en vez de reusar el almacenado, para no depender de que el backfill ya haya corrido.
func (c *ConflictService) candidateIDs(ctx context.Context, mem domain.Memory, cutoff float64) ([]int64, error) {
	var contentVec, titleVec []float32
	if c.titleVectors != nil {
		vectors, err := c.embedder.EmbedBatch(ctx, []string{mem.Content, mem.Title})
		if err != nil {
			return nil, err
		}
		contentVec, titleVec = vectors[0], vectors[1]
	} else {
		v, err := c.embedder.Embed(ctx, mem.Content)
		if err != nil {
			return nil, err
		}
		contentVec = v
	}

	pool := c.topK*candidatePoolFactor + 1
	ordered := make([]int64, 0, pool)
	seen := map[int64]bool{mem.ID: true} // la propia memoria nunca es candidata de sí misma
	appendAbove := func(neighbors []ports.Neighbor, cut float64) {
		for _, n := range neighbors {
			if n.Score < cut {
				break // vecinos ordenados por cercanía: por debajo del corte, el resto también
			}
			if seen[n.MemoryID] {
				continue
			}
			seen[n.MemoryID] = true
			ordered = append(ordered, n.MemoryID)
		}
	}

	contentNeighbors, err := c.vectors.Nearest(ctx, contentVec, pool)
	if err != nil {
		return nil, err
	}
	appendAbove(contentNeighbors, cutoff)

	if c.titleVectors != nil {
		titleNeighbors, err := c.titleVectors.Nearest(ctx, titleVec, pool)
		if err != nil {
			return nil, err
		}
		appendAbove(titleNeighbors, c.titleCutoff)
	}
	return ordered, nil
}

// judgePair invoca al juez LLM sobre un par y delega en resolveAndPersist la resolución. Es la ruta
// del barrido automático; ignora la relación efímera que devuelve (solo le importa persistir el
// conflicto, si lo hay).
func (c *ConflictService) judgePair(ctx context.Context, newMem, cand domain.Memory) error {
	verdict, err := c.runner.Judge(ctx, buildComparePrompt(newMem, cand))
	if err != nil {
		return err
	}
	_, err = c.resolveAndPersist(ctx, newMem, cand, verdict)
	return err
}

// judgeFactualPair es la ruta de la pasada factual (§20.E-bis): invoca al juez y persiste el veredicto
// SOLO si es un reemplazo (supersede). Cualquier otro verbo (conflicts_with, related, compatible,
// scoped, not_conflict) se descarta sin escribir nada: los hechos no disparan banner para un humano
// —el objetivo es únicamente ocultar el hecho caduco cuando otro más reciente lo reemplaza. La
// dirección la resuelve resolveAndPersist vía resolveDirection (mismo scope → gana el más reciente).
func (c *ConflictService) judgeFactualPair(ctx context.Context, newMem, cand domain.Memory) error {
	verdict, err := c.runner.Judge(ctx, buildComparePrompt(newMem, cand))
	if err != nil {
		return err
	}
	// La pasada factual solo oculta hechos caducos: reemplazos (supersedes) y casi-duplicados
	// (duplicate). El resto de verbos no dispara banner para un humano y se descarta.
	if verdict.Relation != domain.RelationSupersedes && verdict.Relation != domain.RelationDuplicate {
		return nil
	}
	_, err = c.resolveAndPersist(ctx, newMem, cand, verdict)
	return err
}

// resolveAndPersist es la lógica compartida por el barrido (judgePair) y mem_compare (Compare): toma
// el veredicto crudo del juez y decide qué hacer con él. Si NO es de conflicto
// (related/compatible/scoped/not_conflict) no persiste nada —no alimenta ni el banner ni la cascada— y
// devuelve una relación efímera (sin id) con el veredicto, útil para informar a quien llamó a
// mem_compare. Si es conflicto, aplica resolveDirection (el scope precede, no el tipo) y persiste la
// relación resuelta de forma idempotente.
func (c *ConflictService) resolveAndPersist(ctx context.Context, newMem, cand domain.Memory, verdict domain.Verdict) (domain.MemoryRelation, error) {
	ephemeral := domain.MemoryRelation{
		SourceID:   newMem.SyncID,
		TargetID:   cand.SyncID,
		Kind:       verdict.Relation,
		Reason:     verdict.Reasoning,
		Confidence: verdict.Confidence,
		Status:     domain.JudgmentJudged,
	}
	// Un casi-duplicado solo se AUTO-OCULTA con confianza alta; por debajo del umbral se deja para
	// revisión humana (Capa 3) y se devuelve efímero, sin ocultar nada por una corazonada del modelo.
	if verdict.Relation == domain.RelationDuplicate && verdict.Confidence < duplicateConfidenceThreshold {
		return ephemeral, nil
	}
	// Los verbos no conflictivos (related/compatible/scoped/not_conflict) no persisten: ni banner ni
	// cascada. Solo conflicts_with, supersedes y duplicate resuelven y se guardan.
	if verdict.Relation != domain.RelationConflictsWith &&
		verdict.Relation != domain.RelationSupersedes &&
		verdict.Relation != domain.RelationDuplicate {
		return ephemeral, nil
	}
	source, target, kind := resolveDirection(newMem, cand, verdict.Relation)
	resolved := domain.Verdict{Relation: kind, Reasoning: verdict.Reasoning, Confidence: verdict.Confidence}
	return c.persistVerdict(ctx, source, target, resolved)
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
	// Casi-duplicado: sobrevive la de mayor CALIDAD (no la más reciente ni la de cierto scope), porque
	// ambas dicen lo mismo y queremos conservar la copia más completa. El sentido lo fija resolveDuplicateDirection.
	if relation == domain.RelationDuplicate {
		return resolveDuplicateDirection(newMem, cand)
	}
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

// resolveDuplicateDirection decide, para un casi-duplicado, cuál memoria sobrevive (source) y cuál se
// oculta (target). Gana la de mayor CALIDAD; el target es el que la recuperación excluye (§20.D). La
// dirección es determinista, así que ambos lados del par producen el mismo sentido (idempotencia).
func resolveDuplicateDirection(a, b domain.Memory) (source, target domain.Memory, kind domain.RelationType) {
	if memoryQualityLess(a, b) {
		return b, a, domain.RelationDuplicate // a es de menor calidad → sobrevive b
	}
	return a, b, domain.RelationDuplicate
}

// memoryQualityLess indica si a es de MENOR calidad que b, para elegir superviviente en un duplicado.
// Criterio, en orden: más contenido (la copia más completa), luego más veces vista/revisada
// (revision_count + duplicate_count), y como desempate final la más reciente.
func memoryQualityLess(a, b domain.Memory) bool {
	if la, lb := len(a.Content), len(b.Content); la != lb {
		return la < lb
	}
	if sa, sb := a.RevisionCount+a.DuplicateCount, b.RevisionCount+b.DuplicateCount; sa != sb {
		return sa < sb
	}
	return a.CreatedAt.Before(b.CreatedAt)
}

// comparable indica si dos normativas son comparables: comparten al menos un proyecto, o alguna es
// global (una preferencia global aplica en todos los proyectos). Evita cruzar decisiones de
// proyectos disjuntos, pero sí compara memorias compartidas entre proyectos acoplados
// (p. ej. "yaku-servidor,yaku-web" contra "yaku-web").
func comparable(a, b domain.Memory) bool {
	if a.Scope == domain.ScopeGlobal || b.Scope == domain.ScopeGlobal {
		return true
	}
	if a.Project == b.Project {
		return true
	}
	return project.OverlapProjects(a.Project, b.Project)
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

// persistVerdict persiste la relación juzgada entre dos memorias (referenciadas por su sync_id) de
// forma IDEMPOTENTE: si ya existe una relación juzgada del par (en cualquier dirección), la actualiza
// en vez de insertar otra; si no, inserta una nueva. Así el par nunca acumula filas redundantes,
// vengan del barrido (ambos lados), de mem_compare o de mem_judge. Todo va dentro de una transacción.
func (c *ConflictService) persistVerdict(ctx context.Context, source, target domain.Memory, verdict domain.Verdict) (domain.MemoryRelation, error) {
	now := c.clock.Now().UTC()
	var result domain.MemoryRelation
	err := c.uow.Do(ctx, func(ctx context.Context) error {
		existing, err := c.relations.FindJudgedPair(ctx, source.SyncID, target.SyncID)
		if err != nil {
			return err
		}
		if existing != nil {
			// Actualiza en sitio: refresca verbo, motivo y confianza sin duplicar. Conserva la
			// dirección original (para conflicts_with es simétrica; para supersedes resolveDirection es
			// determinista, así que ambos lados producen el mismo sentido).
			if err := c.relations.Judge(ctx, existing.ID, verdict); err != nil {
				return err
			}
			existing.Kind = verdict.Relation
			existing.Reason = verdict.Reasoning
			existing.Confidence = verdict.Confidence
			existing.Status = domain.JudgmentJudged
			existing.UpdatedAt = now
			result = *existing
			return nil
		}
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
		id, err := c.relations.Insert(ctx, relation)
		if err != nil {
			return err
		}
		relation.ID = id
		result = relation
		return nil
	})
	if err != nil {
		return domain.MemoryRelation{}, err
	}
	return result, nil
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

Primero decide si son LA MISMA cosa. Dos memorias son "duplicate" si dicen el mismo conocimiento
reformulado y ninguna aporta información sustancial que la otra no tenga; si cada una aporta algo
distinto, NO es duplicate (será compatible o related). Si una ACTUALIZA o corrige a la otra (info
más nueva/mejor), es supersedes, no duplicate.

Elige EXACTAMENTE UNA relación de este vocabulario cerrado:
- duplicate        — dicen lo MISMO reformulado; ninguna añade nada sustancial que la otra no tenga
- conflicts_with   — afirman cosas contradictorias
- supersedes       — una reemplaza, actualiza o corrige a la otra
- scoped           — una es un caso más específico de la otra
- related           — comparten tema pero no se contradicen
- compatible       — son consistentes y se complementan (cada una aporta algo distinto)
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
