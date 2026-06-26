package service

import "github.com/javierv-dev/kandel/internal/domain"

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
