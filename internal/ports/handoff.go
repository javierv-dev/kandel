package ports

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
)

type HandoffRepo interface {
	Add(context.Context, domain.Handoff) error
	Get(context.Context, string, string) (domain.Handoff, error)
	Latest(context.Context, string, string) (domain.Handoff, error)
	List(context.Context, string, int) ([]domain.Handoff, error)
	Resume(context.Context, string, string) error
}
