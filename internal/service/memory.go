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
	// SaveMerged indica que se actualizó una memoria existente del mismo tema (topic_key).
	SaveMerged SaveOutcome = "merged"
	// SaveDuplicate indica que llegó un duplicado exacto reciente; solo se contó.
	SaveDuplicate SaveOutcome = "duplicate"
)

// SaveInput son los datos crudos de una memoria a guardar. El servicio los normaliza y valida.
type SaveInput struct {
	SessionID string
	Project   string
	Type      domain.MemoryType
	Scope     domain.Scope
	Title     string
	Content   string
	ToolName  string
	TopicKey  string // crudo; se normaliza y valida
}

// SaveResult es el resultado de Save: la memoria resultante y qué ocurrió.
type SaveResult struct {
	Memory  domain.Memory
	Outcome SaveOutcome
}

// UpdateInput son los campos editables de una memoria; los nil no se tocan.
type UpdateInput struct {
	Type     *domain.MemoryType
	Title    *string
	Content  *string
	Scope    *domain.Scope
	TopicKey *string
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
}

// NewMemoryService construye el servicio con sus dependencias inyectadas. El embedder y el índice
// vectorial son obligatorios: el guardado calcula y persiste el vector en la misma transacción,
// y la búsqueda semántica los necesita.
func NewMemoryService(memories ports.MemoryRepo, sync ports.SyncRepo, uow ports.UnitOfWork, clock ports.Clock, embedder ports.Embedder, vectors ports.VectorIndex) *MemoryService {
	return &MemoryService{
		memories:     memories,
		sync:         sync,
		uow:          uow,
		clock:        clock,
		embedder:     embedder,
		vectors:      vectors,
		dedupeWindow: defaultDedupeWindow,
	}
}

// Save guarda una memoria curada. Reglas, en orden:
//  1. Si trae topic_key y ya existe una memoria viva de ese tema, la actualiza en su lugar
//     (sube revision_count) en vez de crear un duplicado.
//  2. Si no, y existe una memoria idéntica (mismo hash) creada dentro de la ventana de dedupe,
//     solo incrementa su duplicate_count.
//  3. En otro caso crea una memoria nueva, fijando su review_after según el decaimiento del tipo.
//
// Todo ocurre dentro de una transacción junto con el encolado de la mutación de sync.
func (m *MemoryService) Save(ctx context.Context, in SaveInput) (SaveResult, error) {
	scope := in.Scope
	if scope == "" {
		scope = domain.ScopeProject
	}
	project := normalizeProject(in.Project)
	// Escritura estricta: una memoria de proyecto necesita proyecto resoluble.
	if scope == domain.ScopeProject && project == "" {
		return SaveResult{}, domain.ErrAmbiguousProject
	}

	var topic domain.TopicKey
	if strings.TrimSpace(in.TopicKey) != "" {
		tk, err := domain.ParseTopicKey(normalizeTopicKey(in.TopicKey))
		if err != nil {
			return SaveResult{}, err
		}
		topic = tk
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
		TopicKey:       topic,
		NormalizedHash: hash,
		RevisionCount:  1,
		DuplicateCount: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastSeenAt:     now,
	}
	if err := candidate.Validate(); err != nil {
		return SaveResult{}, err
	}

	var result SaveResult
	err := m.uow.Do(ctx, func(ctx context.Context) error {
		// 1. Fusión por tema.
		if !topic.IsZero() {
			existing, err := m.memories.LatestByTopicKey(ctx, project, scope, topic)
			if err != nil {
				return err
			}
			if existing != nil {
				existing.Type = in.Type
				existing.Title = title
				existing.Content = content
				existing.ToolName = in.ToolName
				existing.NormalizedHash = hash
				existing.RevisionCount++
				existing.UpdatedAt = now
				existing.LastSeenAt = now
				updated, err := m.memories.Update(ctx, existing.ID, *existing)
				if err != nil {
					return err
				}
				// El contenido cambió con la revisión → recalcular su vector.
				if err := m.indexVector(ctx, updated.ID, updated.Content); err != nil {
					return err
				}
				result = SaveResult{Memory: *updated, Outcome: SaveMerged}
				return m.enqueueUpsert(ctx, *updated)
			}
		}

		// 2. Dedupe exacto dentro de la ventana.
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

		// 3. Memoria nueva.
		candidate.SyncID = newSyncID("mem")
		candidate.ReviewAfter = domain.ReviewAfterFor(in.Type, now)
		id, err := m.memories.Add(ctx, candidate)
		if err != nil {
			return err
		}
		candidate.ID = id
		// El embedder es obligatorio: si falla, la transacción revierte y no queda una memoria
		// sin vector buscable (§6.bis del plan).
		if err := m.indexVector(ctx, id, candidate.Content); err != nil {
			return err
		}
		result = SaveResult{Memory: candidate, Outcome: SaveCreated}
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
		if in.TopicKey != nil {
			if strings.TrimSpace(*in.TopicKey) == "" {
				o.TopicKey = ""
			} else {
				tk, err := domain.ParseTopicKey(normalizeTopicKey(*in.TopicKey))
				if err != nil {
					return err
				}
				o.TopicKey = tk
			}
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

// Delete borra una memoria (soft por defecto, hard si se pide) y encola la mutación de borrado.
func (m *MemoryService) Delete(ctx context.Context, id int64, hard bool) error {
	return m.uow.Do(ctx, func(ctx context.Context) error {
		o, err := m.memories.Get(ctx, id)
		if err != nil {
			return err
		}
		if o == nil {
			return domain.ErrNotFound
		}
		if hard {
			err = m.memories.HardDelete(ctx, id)
		} else {
			err = m.memories.SoftDelete(ctx, id)
		}
		if err != nil {
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
