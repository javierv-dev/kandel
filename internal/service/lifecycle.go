package service

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// LifecycleService implementa los casos de uso del ciclo de vida de las memorias: fijado y
// revisión por decaimiento. El fijado (pin) es estado LOCAL: no se sincroniza, por eso aquí no
// hay encolado de mutaciones ni transacción.
type LifecycleService struct {
	memories ports.MemoryRepo
}

// NewLifecycleService construye el servicio con sus dependencias inyectadas.
func NewLifecycleService(memories ports.MemoryRepo) *LifecycleService {
	return &LifecycleService{memories: memories}
}

// Pin fija una memoria arriba en el contexto.
func (l *LifecycleService) Pin(ctx context.Context, id int64) error {
	return l.memories.SetPinned(ctx, id, true)
}

// Unpin desfija una memoria.
func (l *LifecycleService) Unpin(ctx context.Context, id int64) error {
	return l.memories.SetPinned(ctx, id, false)
}

// NeedingReview lista las memorias del proyecto cuya fecha de revisión ya venció (decaimiento).
func (l *LifecycleService) NeedingReview(ctx context.Context, project string, limit int) ([]domain.Memory, error) {
	return l.memories.NeedingReview(ctx, normalizeProject(project), limit)
}
