package hook

import (
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

// rootDirProjectName devuelve el nombre normalizado de la carpeta raíz del proyecto (la raíz con .git
// si existe, o el propio cwd si no es un repositorio git). A diferencia del antiguo oldProjectName
// (que tomaba filepath.Base(cwd) sin subir a la raíz git), iniciar sesión desde una subcarpeta como
// "repo/internal" devuelve el nombre de la carpeta raíz "repo" y no "internal". Devuelve "" si cwd está vacío.
func rootDirProjectName(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	return project.Detect(cwd).DirName
}
