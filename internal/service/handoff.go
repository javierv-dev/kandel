package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/project"
)

type HandoffService struct {
	repo     ports.HandoffRepo
	sessions ports.SessionRepo
	uow      ports.UnitOfWork
	clock    ports.Clock
}

func NewHandoffService(repo ports.HandoffRepo, sessions ports.SessionRepo, uow ports.UnitOfWork, clock ports.Clock) *HandoffService {
	return &HandoffService{repo, sessions, uow, clock}
}

func (s *HandoffService) session(ctx context.Context, id string) (*domain.Session, error) {
	if id == "" {
		return nil, fmt.Errorf("session_id es obligatorio; no se infiere la última conversación")
	}
	session, err := s.sessions.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, domain.ErrUnknownSession
	}
	return session, nil
}

// HandoffInput appends to ParentID. RequestID must be reused on transport retries.
type HandoffInput struct{ SessionID, ParentID, RequestID, Title, Content string }

func (s *HandoffService) Save(ctx context.Context, in HandoffInput) (domain.Handoff, error) {
	in.Title, in.Content = strings.TrimSpace(in.Title), strings.TrimSpace(in.Content)
	if in.Title == "" || in.Content == "" || utf8.RuneCountInString(in.Title) > 120 || utf8.RuneCountInString(in.Content) > 2200 {
		return domain.Handoff{}, fmt.Errorf("título y contenido requeridos (máximo 120 y 2200 caracteres)")
	}
	if strings.TrimSpace(in.RequestID) == "" || len(in.RequestID) > 128 {
		return domain.Handoff{}, fmt.Errorf("request_id obligatorio (máximo 128 bytes)")
	}
	session, err := s.session(ctx, in.SessionID)
	if err != nil {
		return domain.Handoff{}, err
	}
	tree, err := project.Worktree(session.Directory)
	if err != nil {
		return domain.Handoff{}, err
	}
	id := project.LocalKey("handoff", session.ID, in.RequestID)
	h := domain.Handoff{ID: id, TaskID: id, ParentID: in.ParentID, Worktree: tree, Title: in.Title, Content: in.Content, SessionID: session.ID, Application: session.Application, CreatedAt: s.clock.Now().UTC()}
	err = s.uow.Do(ctx, func(ctx context.Context) error {
		existing, err := s.repo.Get(ctx, tree, id)
		if err == nil {
			if existing.ParentID != in.ParentID || existing.Title != in.Title || existing.Content != in.Content {
				return fmt.Errorf("request_id ya utilizado para otro checkpoint")
			}
			h = existing
			return nil
		}
		if err != domain.ErrNotFound {
			return err
		}
		if in.ParentID != "" {
			parent, err := s.repo.Get(ctx, tree, in.ParentID)
			if err != nil {
				return err
			}
			latest, err := s.repo.Latest(ctx, tree, parent.TaskID)
			if err != nil {
				return err
			}
			if latest.ID != parent.ID {
				return fmt.Errorf("checkpoint desactualizado; consulta la versión actual (%s)", latest.ID)
			}
			h.TaskID = parent.TaskID
		}
		return s.repo.Add(ctx, h)
	})
	return h, err
}

func (s *HandoffService) directory(ctx context.Context, id string) (string, error) {
	session, err := s.session(ctx, id)
	if err != nil {
		return "", err
	}
	return session.Directory, nil
}

func (s *HandoffService) ListSession(ctx context.Context, id string) ([]domain.Handoff, error) {
	dir, err := s.directory(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.List(ctx, dir)
}

func (s *HandoffService) GetSession(ctx context.Context, session, id string) (domain.Handoff, error) {
	dir, err := s.directory(ctx, session)
	if err != nil {
		return domain.Handoff{}, err
	}
	return s.Get(ctx, dir, id)
}

func (s *HandoffService) List(ctx context.Context, dir string) ([]domain.Handoff, error) {
	tree, err := project.Worktree(dir)
	if err != nil {
		return nil, err
	}
	return s.repo.List(ctx, tree, 10)
}

func (s *HandoffService) Get(ctx context.Context, dir, id string) (domain.Handoff, error) {
	tree, err := project.Worktree(dir)
	if err != nil {
		return domain.Handoff{}, err
	}
	return s.repo.Get(ctx, tree, id)
}

func (s *HandoffService) Resume(ctx context.Context, session, id string) (domain.Handoff, error) {
	h, err := s.GetSession(ctx, session, id)
	if err != nil {
		return h, err
	}
	return h, s.repo.Resume(ctx, h.ID, session)
}
