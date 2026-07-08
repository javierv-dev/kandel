package hook

import (
	"path/filepath"
	"strings"

	"github.com/javierv-dev/kandel/internal/project"
)

// detectProject deduce el nombre del proyecto a partir del directorio de trabajo del agente,
// delegando en el detector único de kandel (remoto origin → raíz git → basename). Devuelve "" si no
// hay nada que deducir. Sin cwd explícito se consulta el directorio del proceso (".") para el git,
// pero no se cae al basename "." (no es un nombre útil).
func detectProject(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		det := project.Detect(".")
		if det.Source == project.SourceDirBasename {
			return ""
		}
		return det.Project
	}
	return project.Detect(cwd).Project
}

// oldProjectName es el nombre "anterior" del proyecto (basename del cwd) que se usa para detectar si
// el proyecto cambió de nombre y disparar la migración. Devuelve "" si no hay cwd.
func oldProjectName(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	return project.Normalize(filepath.Base(cwd))
}
