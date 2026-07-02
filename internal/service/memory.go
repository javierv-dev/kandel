package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// defaultDedupeWindow es la ventana en la que dos memorias idénticas (mismo hash) se consideran
// el mismo recuerdo repetido en vez de una memoria nueva.
const defaultDedupeWindow = 15 * time.Minute

// SaveOutcome describe qué hizo Save con la memoria entrante.
type SaveOutcome string

const (
	// SaveCreated indica que se creó una memoria nueva.
	SaveCreated SaveOutcome = "created"
	// SaveDuplicate indica que llegó un duplicado exacto reciente; solo se contó.
	SaveDuplicate SaveOutcome = "duplicate"
)

// SaveInput son los datos crudos de una memoria a guardar. El servicio los normaliza y valida.
type SaveInput struct {
	SessionID  string
	Project    string
	Type       domain.MemoryType
	Scope      domain.Scope
	Title      string
	Content    string
	ToolName   string
	Resolution domain.Resolution
}

// SaveResult es el resultado de Save: la memoria resultante y qué ocurrió.
type SaveResult struct {
	Memory  domain.Memory
	Outcome SaveOutcome
	// EmbeddingDeferred indica que la memoria se guardó SIN vector porque el embebedor no estaba
	// disponible (p. ej. Ollama caído). La memoria es válida y accesible por lecturas directas; solo
	// queda pendiente de aparecer en la búsqueda semántica hasta que un backfill posterior le calcule
	// el vector. Las capas de presentación (MCP/HTTP/CLI) lo reflejan en su respuesta.
	EmbeddingDeferred bool
}

// UpdateInput son los campos editables de una memoria; los nil no se tocan.
type UpdateInput struct {
	Type       *domain.MemoryType
	Title      *string
	Content    *string
	Scope      *domain.Scope
	Resolution *domain.Resolution
}

// MemoryService implementa los casos de uso sobre memorias. Aquí vive la lógica de dedupe
// exacto, fusión por tema y decaimiento, fuera del SQL y testeable con repos fake.
// searchPoolFactor amplía el número de vecinos que se piden al índice vectorial respecto al
// límite final, porque después se filtran por proyecto/alcance/tipo y algunos se descartan.
const searchPoolFactor = 5

// minSearchPool es el tamaño mínimo del conjunto de candidatos a rankear.
const minSearchPool = 50

// MemoryService implementa los casos de uso sobre memorias. Aquí vive la lógica de dedupe
// exacto, fusión por tema y decaimiento, fuera del SQL y testeable con repos fake.
type MemoryService struct {
	memories     ports.MemoryRepo
	sync         ports.SyncRepo
	uow          ports.UnitOfWork
	clock        ports.Clock
	embedder     ports.Embedder
	vectors      ports.VectorIndex
	dedupeWindow time.Duration
	warnf        func(format string, args ...any)
}

// MemoryOption configura un MemoryService opcionalmente (sin romper a quienes ya llaman al
// constructor sin opciones).
type MemoryOption func(*MemoryService)

// WithWarnLogger inyecta un destino para los avisos del servicio (p. ej. "memoria guardada sin
// vector porque el embebedor está caído"). Por defecto es no-op, así los tests no necesitan logger.
func WithWarnLogger(warnf func(format string, args ...any)) MemoryOption {
	return func(m *MemoryService) {
		if warnf != nil {
			m.warnf = warnf
		}
	}
}

// NewMemoryService construye el servicio con sus dependencias inyectadas. El embedder y el índice
// vectorial son obligatorios para la búsqueda semántica; el guardado, en cambio, es TOLERANTE: si el
// embebedor falla, la memoria se guarda igual (sin vector) y queda pendiente de backfill.
func NewMemoryService(memories ports.MemoryRepo, sync ports.SyncRepo, uow ports.UnitOfWork, clock ports.Clock, embedder ports.Embedder, vectors ports.VectorIndex, opts ...MemoryOption) *MemoryService {
	m := &MemoryService{
		memories:     memories,
		sync:         sync,
		uow:          uow,
		clock:        clock,
		embedder:     embedder,
		vectors:      vectors,
		dedupeWindow: defaultDedupeWindow,
		warnf:        func(string, ...any) {}, // no-op por defecto
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Save guarda una memoria curada. Reglas, en orden:
//  1. Si existe una memoria idéntica (mismo hash) creada dentro de la ventana de dedupe,
//     solo incrementa su duplicate_count.
//  2. En otro caso crea una memoria nueva, con el estado de pendiente (resolution) indicado.
//
// Todo ocurre dentro de una transacción junto con el encolado de la mutación de sync.
func (m *MemoryService) Save(ctx context.Context, in SaveInput) (SaveResult, error) {
	scope := in.Scope
	if scope == "" {
		scope = domain.DefaultScopeForType(in.Type)
	}
	project := normalizeProject(in.Project)
	// Escritura estricta: una memoria de proyecto necesita proyecto resoluble.
	if scope == domain.ScopeProject && project == "" {
		return SaveResult{}, domain.ErrAmbiguousProject
	}

	now := m.clock.Now().UTC()
	title := strings.TrimSpace(in.Title)
	content := strings.TrimSpace(in.Content)
	hash := hashContent(content)

	candidate := domain.Memory{
		SessionID:      in.SessionID,
		Project:        project,
		Type:           in.Type,
		Scope:          scope,
		Title:          title,
		Content:        content,
		ToolName:       in.ToolName,
		NormalizedHash: hash,
		RevisionCount:  1,
		DuplicateCount: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastSeenAt:     now,
		Resolution:     in.Resolution,
	}
	if err := candidate.Validate(); err != nil {
		return SaveResult{}, err
	}

	var result SaveResult
	err := m.uow.Do(ctx, func(ctx context.Context) error {
		// 1. Dedupe exacto dentro de la ventana.
		dup, err := m.memories.FindByHash(ctx, project, scope, hash)
		if err != nil {
			return err
		}
		if dup != nil && now.Sub(dup.CreatedAt) <= m.dedupeWindow {
			dup.DuplicateCount++
			dup.LastSeenAt = now
			dup.UpdatedAt = now
			updated, err := m.memories.Update(ctx, dup.ID, *dup)
			if err != nil {
				return err
			}
			result = SaveResult{Memory: *updated, Outcome: SaveDuplicate}
			return m.enqueueUpsert(ctx, *updated)
		}

		// 2. Memoria nueva.
		candidate.SyncID = newSyncID("mem")
		id, err := m.memories.Add(ctx, candidate)
		if err != nil {
			return err
		}
		candidate.ID = id
		// Save es tolerante: si el embebedor falla, la memoria se guarda igual (sin vector) y queda
		// pendiente de backfill (§6.bis del plan, "Tolerancia a embebedor caído").
		deferred, err := m.indexVectorTolerant(ctx, id, candidate.Content)
		if err != nil {
			return err
		}
		result = SaveResult{Memory: candidate, Outcome: SaveCreated, EmbeddingDeferred: deferred}
		return m.enqueueUpsert(ctx, candidate)
	})
	if err != nil {
		return SaveResult{}, err
	}
	return result, nil
}

// Get recupera una memoria por id.
func (m *MemoryService) Get(ctx context.Context, id int64) (*domain.Memory, error) {
	return m.memories.Get(ctx, id)
}

// Recent lista memorias recientes (navegación del usuario).
func (m *MemoryService) Recent(ctx context.Context, q ports.RecentQuery) ([]domain.Memory, error) {
	return m.memories.Recent(ctx, q)
}

// Search es la búsqueda semántica del agente: convierte la consulta en vector, pide los vecinos
// más cercanos al índice y los hidrata a memorias aplicando los filtros de q. Pide un conjunto
// de candidatos más amplio que el límite final porque luego se filtran por proyecto/alcance/tipo.
func (m *MemoryService) Search(ctx context.Context, q ports.SearchQuery) ([]domain.SearchResult, error) {
	vector, err := m.embedder.Embed(ctx, q.Query)
	if err != nil {
		return nil, err
	}
	pool := q.Limit * searchPoolFactor
	if pool < minSearchPool {
		pool = minSearchPool
	}
	neighbors, err := m.vectors.Nearest(ctx, vector, pool)
	if err != nil {
		return nil, err
	}
	return m.memories.Search(ctx, q, neighbors)
}

// indexVector calcula el vector del contenido y lo guarda en el índice, anotando el modelo.
func (m *MemoryService) indexVector(ctx context.Context, memoryID int64, content string) error {
	vector, err := m.embedder.Embed(ctx, content)
	if err != nil {
		return err
	}
	return m.vectors.Upsert(ctx, memoryID, vector, m.embedder.Model())
}

// indexVectorTolerant intenta calcular y persistir el vector, pero NO aborta la transacción si el
// embebedor falla: la memoria se conserva sin vector (válida y accesible por lecturas directas) y
// queda pendiente de backfill, que un proceso posterior resolverá. Devuelve true si el embebedor
// falló (vector diferido), registrando un aviso. Un error de la propia escritura del vector (la DB,
// no el embebedor) sí se propaga: ahí no hay tolerancia que aplicar.
func (m *MemoryService) indexVectorTolerant(ctx context.Context, memoryID int64, content string) (deferred bool, err error) {
	vector, embErr := m.embedder.Embed(ctx, content)
	if embErr != nil {
		m.warnf("memoria #%d guardada SIN vector (embebedor no disponible: %v); pendiente de backfill", memoryID, embErr)
		return true, nil
	}
	if err := m.vectors.Upsert(ctx, memoryID, vector, m.embedder.Model()); err != nil {
		return false, err
	}
	return false, nil
}

// Update aplica cambios parciales a una memoria viva y reencola su mutación. Si cambia el
// contenido, recalcula el hash. Devuelve domain.ErrNotFound si la memoria no existe.
func (m *MemoryService) Update(ctx context.Context, id int64, in UpdateInput) (domain.Memory, error) {
	now := m.clock.Now().UTC()
	var result domain.Memory
	err := m.uow.Do(ctx, func(ctx context.Context) error {
		o, err := m.memories.Get(ctx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return domain.ErrNotFound
		}
		if in.Type != nil {
			o.Type = *in.Type
		}
		if in.Title != nil {
			o.Title = strings.TrimSpace(*in.Title)
		}
		if in.Content != nil {
			o.Content = strings.TrimSpace(*in.Content)
			o.NormalizedHash = hashContent(o.Content)
		}
		if in.Scope != nil {
			o.Scope = *in.Scope
		}
		if in.Resolution != nil {
			o.Resolution = *in.Resolution
		}
		// Si cambió el contenido de una normativa, su análisis de conflictos quedó obsoleto: se
		// marca para re-verificar en el próximo barrido de detección.
		if in.Content != nil && o.Type.IsNormative() {
			o.ConflictChecked = false
		}
		o.UpdatedAt = now
		if err := o.Validate(); err != nil {
			return err
		}
		updated, err := m.memories.Update(ctx, id, *o)
		if err != nil {
			return err
		}
		if updated == nil {
			return domain.ErrNotFound
		}
		result = *updated
		return m.enqueueUpsert(ctx, *updated)
	})
	if err != nil {
		return domain.Memory{}, err
	}
	return result, nil
}

// Delete borra una memoria de forma LÓGICA (soft-delete, reversible con Restore) y encola la
// mutación de borrado. No existe borrado físico: la fila siempre sobrevive para poder restaurarla.
func (m *MemoryService) Delete(ctx context.Context, id int64) error {
	return m.uow.Do(ctx, func(ctx context.Context) error {
		o, err := m.memories.Get(ctx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return domain.ErrNotFound
		}
		if err := m.memories.SoftDelete(ctx, id); err != nil {
			return err
		}
		return m.enqueue(ctx, *o, domain.SyncOpDelete)
	})
}

// Restore revierte un soft-delete y vuelve a encolar la memoria como alta para el sync (deshace
// la mutación de borrado propagada). Devuelve la memoria restaurada. domain.ErrNotFound si la
// memoria no existe o no estaba borrada.
func (m *MemoryService) Restore(ctx context.Context, id int64) (domain.Memory, error) {
	var restored domain.Memory
	err := m.uow.Do(ctx, func(ctx context.Context) error {
		if err := m.memories.Restore(ctx, id); err != nil {
			return err
		}
		o, err := m.memories.Get(ctx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return domain.ErrNotFound
		}
		restored = *o
		return m.enqueueUpsert(ctx, *o)
	})
	return restored, err
}

// enqueueUpsert encola una mutación de alta/actualización para una memoria.
func (m *MemoryService) enqueueUpsert(ctx context.Context, o domain.Memory) error {
	return m.enqueue(ctx, o, domain.SyncOpUpsert)
}

// enqueue construye y encola la mutación de sync de una memoria.
func (m *MemoryService) enqueue(ctx context.Context, o domain.Memory, op domain.SyncOp) error {
	payload, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return m.sync.EnqueueMutation(ctx, domain.SyncMutation{
		TargetKey:  defaultSyncTarget,
		Entity:     domain.SyncEntityMemory,
		EntityKey:  o.SyncID,
		Op:         op,
		Payload:    payload,
		Source:     sourceLocal,
		Project:    o.Project,
		OccurredAt: m.clock.Now().UTC(),
	})
}
