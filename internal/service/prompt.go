package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// PromptService implementa los casos de uso sobre prompts: el contexto crudo de lo que el
// usuario pidió, guardado aparte de las memorias curadas. El guardado es idempotente (no duplica
// un prompt idéntico de la misma sesión y proyecto) y encola su mutación de sync cuando es nuevo.
type PromptService struct {
	prompts ports.PromptRepo
	sync    ports.SyncRepo
	uow     ports.UnitOfWork
	clock   ports.Clock
}

// NewPromptService construye el servicio con sus dependencias inyectadas.
func NewPromptService(prompts ports.PromptRepo, sync ports.SyncRepo, uow ports.UnitOfWork, clock ports.Clock) *PromptService {
	return &PromptService{prompts: prompts, sync: sync, uow: uow, clock: clock}
}

// SavePromptInput son los datos de un prompt a guardar.
type SavePromptInput struct {
	SessionID string
	Project   string
	Content   string
}

// SavePromptResult es el resultado de Save: el prompt y si se insertó (true) o ya existía.
type SavePromptResult struct {
	Prompt   domain.Prompt
	Inserted bool
	// SessionDropped indica que el prompt se guardó SIN sesión porque la referenciada no existía.
	// El contenido se conservó; lo que se perdió es el enlace con la conversación.
	SessionDropped bool
}

// Save guarda un prompt de forma idempotente. Si ya existía uno idéntico (misma sesión, proyecto
// y contenido) no inserta nada ni encola mutación; si es nuevo, lo guarda y encola su sync, todo
// dentro de una transacción.
func (s *PromptService) Save(ctx context.Context, in SavePromptInput) (SavePromptResult, error) {
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return SavePromptResult{}, domain.ErrEmptyContent
	}
	now := s.clock.Now().UTC()
	prompt := domain.Prompt{
		SyncID:    newSyncID("prm"),
		SessionID: in.SessionID,
		Project:   normalizeProject(in.Project),
		Content:   content,
		CreatedAt: now,
	}

	result, err := s.persist(ctx, prompt)
	if err == nil {
		return result, nil
	}

	// La sesión referenciada no existe. Antes que perder el prompt, se guarda sin sesión: el
	// contenido es el dato durable —lo que el usuario pidió, que la capa determinista promete
	// registrar siempre— mientras que la sesión es solo el contenedor que lo liga a su
	// conversación, y de hecho el camino de importación por sync ya la descarta a propósito.
	// Rechazar el prompt sacrificaría lo valioso por preservar lo accesorio.
	//
	// No se crea la sesión al vuelo a propósito: aquí no se conocen su directorio ni su instante de
	// arranque reales, y fabricar una fila con datos inventados es peor que no tenerla.
	if errors.Is(err, domain.ErrUnknownSession) && prompt.SessionID != "" {
		prompt.SessionID = ""
		result, err = s.persist(ctx, prompt)
		if err != nil {
			return SavePromptResult{}, err
		}
		result.SessionDropped = true
		return result, nil
	}
	return SavePromptResult{}, err
}

// persist guarda el prompt dentro de una transacción y encola su mutación de sync si es nuevo.
func (s *PromptService) persist(ctx context.Context, prompt domain.Prompt) (SavePromptResult, error) {
	var result SavePromptResult
	err := s.uow.Do(ctx, func(ctx context.Context) error {
		id, inserted, err := s.prompts.AddIfMissing(ctx, prompt)
		if err != nil {
			return err
		}
		prompt.ID = id
		result = SavePromptResult{Prompt: prompt, Inserted: inserted}
		if !inserted {
			// Ya existía: no se reencola para no inflar la cola con duplicados.
			return nil
		}
		return s.enqueue(ctx, prompt)
	})
	if err != nil {
		return SavePromptResult{}, err
	}
	return result, nil
}

// Recent lista los prompts más recientes de un proyecto (lectura directa).
func (s *PromptService) Recent(ctx context.Context, project string, limit int) ([]domain.Prompt, error) {
	return s.prompts.Recent(ctx, normalizeProject(project), limit)
}

// enqueue construye y encola la mutación de sync de un prompt.
func (s *PromptService) enqueue(ctx context.Context, p domain.Prompt) error {
	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.sync.EnqueueMutation(ctx, domain.SyncMutation{
		TargetKey:  defaultSyncTarget,
		Entity:     domain.SyncEntityPrompt,
		EntityKey:  p.SyncID,
		Op:         domain.SyncOpUpsert,
		Payload:    payload,
		Source:     sourceLocal,
		Project:    p.Project,
		OccurredAt: s.clock.Now().UTC(),
	})
}
