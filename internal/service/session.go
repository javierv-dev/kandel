package service

import (
	"context"
	"encoding/json"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// SessionService implementa los casos de uso sobre sesiones.
type SessionService struct {
	sessions ports.SessionRepo
	sync     ports.SyncRepo
	uow      ports.UnitOfWork
	clock    ports.Clock
}

// NewSessionService construye el servicio con sus dependencias inyectadas.
func NewSessionService(sessions ports.SessionRepo, sync ports.SyncRepo, uow ports.UnitOfWork, clock ports.Clock) *SessionService {
	return &SessionService{sessions: sessions, sync: sync, uow: uow, clock: clock}
}

// StartInput son los datos para abrir una sesión.
type StartInput struct {
	ID             string
	Project        string
	Directory      string
	Application    string
	ConversationID string
}

// Start abre una sesión nueva y encola su mutación, todo de forma atómica. Es idempotente: si el
// id ya existe (los hooks reaseguran la sesión en cada prompt), devuelve la existente sin tocarla,
// sin reabrirla y sin encolar otra mutación.
func (s *SessionService) Start(ctx context.Context, in StartInput) (domain.Session, error) {
	session := domain.Session{
		ID:             in.ID,
		Project:        normalizeProject(in.Project),
		Directory:      in.Directory,
		Application:    in.Application,
		ConversationID: in.ConversationID,
		StartedAt:      s.clock.Now().UTC(),
	}
	err := s.uow.Do(ctx, func(ctx context.Context) error {
		existing, err := s.sessions.Get(ctx, session.ID)
		if err != nil {
			return err
		}
		if existing != nil {
			session = *existing
			return nil
		}
		if err := s.sessions.Create(ctx, session); err != nil {
			return err
		}
		return s.enqueue(ctx, session)
	})
	if err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

// End cierra una sesión y reencola su mutación. Devuelve domain.ErrNotFound si la
// sesión no existe.
func (s *SessionService) End(ctx context.Context, id string) error {
	return s.uow.Do(ctx, func(ctx context.Context) error {
		if err := s.sessions.End(ctx, id); err != nil {
			return err
		}
		session, err := s.sessions.Get(ctx, id)
		if err != nil {
			return err
		}
		if session == nil {
			return domain.ErrNotFound
		}
		return s.enqueue(ctx, *session)
	})
}

// enqueue construye y encola la mutación de sync de una sesión.
func (s *SessionService) enqueue(ctx context.Context, session domain.Session) error {
	payload, err := json.Marshal(session)
	if err != nil {
		return err
	}
	return s.sync.EnqueueMutation(ctx, domain.SyncMutation{
		TargetKey:  defaultSyncTarget,
		Entity:     domain.SyncEntitySession,
		EntityKey:  session.ID,
		Op:         domain.SyncOpUpsert,
		Payload:    payload,
		Source:     sourceLocal,
		Project:    session.Project,
		OccurredAt: s.clock.Now().UTC(),
	})
}
