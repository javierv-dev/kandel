package service

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// LifecycleService implementa los casos de uso del ciclo de vida de las memorias: revisión por
// decaimiento. No hay encolado de mutaciones ni transacción: solo lectura del repositorio.
type LifecycleService struct {
	memories ports.MemoryRepo
}

// NewLifecycleService construye el servicio con sus dependencias inyectadas.
func NewLifecycleService(memories ports.MemoryRepo) *LifecycleService {
	return &LifecycleService{memories: memories}
}

// NeedingReview lista las memorias del proyecto cuya fecha de revisión ya venció (decaimiento).
func (l *LifecycleService) NeedingReview(ctx context.Context, project string, limit int) ([]domain.Memory, error) {
	return l.memories.NeedingReview(ctx, normalizeProject(project), limit)
}
