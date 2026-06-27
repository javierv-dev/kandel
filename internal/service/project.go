package service

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// ProjectService resuelve el nombre de proyecto con rigidez asimétrica: la ESCRITURA es estricta
// (debe quedar un proyecto inequívoco, o falla) y la LECTURA es laxa (un proyecto vacío significa
// "todos", para navegar sin fricción). Es deliberado: equivocar el proyecto al guardar contamina
// la memoria; al leer, como mucho, muestra de más.
type ProjectService struct{}

// NewProjectService construye el resolutor de proyecto.
func NewProjectService() ProjectService {
	return ProjectService{}
}

// ResolveForWrite normaliza el proyecto para una escritura. Devuelve ErrAmbiguousProject si tras
// normalizar queda vacío: no se permite guardar sin un proyecto claro.
func (ProjectService) ResolveForWrite(explicit string) (string, error) {
	n := normalizeProject(explicit)
	if n == "" {
		return "", domain.ErrAmbiguousProject
	}
	return n, nil
}

// ResolveForRead normaliza el proyecto para una lectura. Una cadena vacía es válida y significa
// "sin filtrar por proyecto".
func (ProjectService) ResolveForRead(explicit string) string {
	return normalizeProject(explicit)
}

// ProjectMigrator renombra un proyecto en todas sus entidades. Lo dispara el hook de inicio de
// sesión cuando el nombre derivado del remoto git difiere del basename con el que se guardaba.
type ProjectMigrator struct {
	memories ports.MemoryRepo
	sessions ports.SessionRepo
	prompts  ports.PromptRepo
	uow      ports.UnitOfWork
}

// NewProjectMigrator construye el migrador con sus dependencias inyectadas.
func NewProjectMigrator(memories ports.MemoryRepo, sessions ports.SessionRepo, prompts ports.PromptRepo, uow ports.UnitOfWork) *ProjectMigrator {
	return &ProjectMigrator{memories: memories, sessions: sessions, prompts: prompts, uow: uow}
}

// MigrateResult resume cuántas filas se renombraron por entidad. Migrated es false cuando no había
// nada que migrar (nombres iguales tras normalizar, o sin filas del proyecto viejo).
type MigrateResult struct {
	Migrated        bool
	MemoriesUpdated int64
	SessionsUpdated int64
	PromptsUpdated  int64
}

// Migrate renombra un proyecto en memorias, sesiones y prompts dentro de una sola transacción.
// Normaliza ambos nombres con la misma regla del guardado; si coinciden (p. ej. difieren solo en
// mayúsculas) no hace nada para no crear duplicados. La migración es local: no re-encola
// mutaciones de sync (coherente con que el sync actual es de memorias por sync_id). Ver §18 del plan.
func (s *ProjectMigrator) Migrate(ctx context.Context, oldProject, newProject string) (MigrateResult, error) {
	old := normalizeProject(oldProject)
	neu := normalizeProject(newProject)
	if old == "" || neu == "" || old == neu {
		return MigrateResult{}, nil
	}

	var result MigrateResult
	err := s.uow.Do(ctx, func(ctx context.Context) error {
		m, err := s.memories.UpdateProject(ctx, old, neu)
		if err != nil {
			return err
		}
		ss, err := s.sessions.UpdateProject(ctx, old, neu)
		if err != nil {
			return err
		}
		p, err := s.prompts.UpdateProject(ctx, old, neu)
		if err != nil {
			return err
		}
		result = MigrateResult{
			Migrated:        m+ss+p > 0,
			MemoriesUpdated: m,
			SessionsUpdated: ss,
			PromptsUpdated:  p,
		}
		return nil
	})
	if err != nil {
		return MigrateResult{}, err
	}
	return result, nil
}
