package service

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// LifecycleService implementa los casos de uso del ciclo de vida de las memorias: las pendientes
// sin resolver. No hay encolado de mutaciones ni transacción: solo lectura del repositorio.
type LifecycleService struct {
	memories ports.MemoryRepo
}

// NewLifecycleService construye el servicio con sus dependencias inyectadas.
func NewLifecycleService(memories ports.MemoryRepo) *LifecycleService {
	return &LifecycleService{memories: memories}
}

// PendingResolution lista las memorias del proyecto pendientes por resolver (resolution = open).
func (l *LifecycleService) PendingResolution(ctx context.Context, project string, limit int) ([]domain.Memory, error) {
	return l.memories.PendingResolution(ctx, normalizeProject(project), limit)
}
