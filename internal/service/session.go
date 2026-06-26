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
	ID        string
	Project   string
	Directory string
}

// Start abre una sesión nueva y encola su mutación, todo de forma atómica.
func (s *SessionService) Start(ctx context.Context, in StartInput) (domain.Session, error) {
	session := domain.Session{
		ID:        in.ID,
		Project:   normalizeProject(in.Project),
		Directory: in.Directory,
		StartedAt: s.clock.Now().UTC(),
	}
	err := s.uow.Do(ctx, func(ctx context.Context) error {
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

// End cierra una sesión con un resumen y reencola su mutación. Devuelve domain.ErrNotFound si la
// sesión no existe.
func (s *SessionService) End(ctx context.Context, id, summary string) error {
	return s.uow.Do(ctx, func(ctx context.Context) error {
		if err := s.sessions.End(ctx, id, summary); err != nil {
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

// Get recupera una sesión por id.
func (s *SessionService) Get(ctx context.Context, id string) (*domain.Session, error) {
	return s.sessions.Get(ctx, id)
}

// MostRecentActive devuelve la sesión abierta más reciente del proyecto, resolviendo el nombre.
func (s *SessionService) MostRecentActive(ctx context.Context, project string) (string, bool, error) {
	return s.sessions.MostRecentActive(ctx, normalizeProject(project))
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
