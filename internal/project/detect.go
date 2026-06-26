package project

import (
	"os"
	"path/filepath"
	"strings"
)

// Source indica de dónde salió el nombre de proyecto detectado, para que quien lo use sepa cuán
// fiable es.
type Source string

const (
	// SourceGitRoot: el nombre es el basename de la raíz del repositorio git (hay un .git).
	SourceGitRoot Source = "git_root"
	// SourceDirBasename: no se halló raíz git; el nombre es el basename del directorio.
	SourceDirBasename Source = "dir_basename"
	// SourceNone: no se pudo determinar (directorio vacío).
	SourceNone Source = "none"
)

// Detection es el resultado de detectar el proyecto de un directorio.
type Detection struct {
	Project string // nombre normalizado; vacío si no se pudo determinar
	Source  Source // de dónde salió
	Path    string // directorio que dio el nombre (raíz git o el propio dir)
}

// Detect resuelve el proyecto de un directorio: si está dentro de un repositorio git, usa el
// basename de la raíz del repo (subir hasta encontrar .git); si no, el basename del directorio.
//
// Nota: es una detección deliberadamente simple respecto al proyecto viejo (no lee el remoto de
// git ni configuraciones por proyecto). Cubre el caso común; la cadena completa se portará si se
// necesita (ver BITACORA).
func Detect(dir string) Detection {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return Detection{Source: SourceNone}
	}
	if root, ok := gitRoot(dir); ok {
		return Detection{Project: normalize(filepath.Base(root)), Source: SourceGitRoot, Path: root}
	}
	return Detection{Project: normalize(filepath.Base(dir)), Source: SourceDirBasename, Path: dir}
}

// DetectCWD detecta el proyecto del directorio de trabajo actual.
func DetectCWD() Detection {
	cwd, err := os.Getwd()
	if err != nil {
		return Detection{Source: SourceNone}
	}
	return Detect(cwd)
}

// gitRoot sube por el árbol de directorios buscando una entrada .git. Devuelve el directorio que
// la contiene y true, o false si llega a la raíz del sistema sin encontrarla.
func gitRoot(dir string) (string, bool) {
	current := dir
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		current = parent
	}
}

// normalize aplica la forma canónica del nombre de proyecto: minúsculas, recortado y con
// guiones/guiones bajos consecutivos colapsados. Misma regla que el servicio.
func normalize(name string) string {
	n := strings.TrimSpace(strings.ToLower(name))
	for strings.Contains(n, "--") {
		n = strings.ReplaceAll(n, "--", "-")
	}
	for strings.Contains(n, "__") {
		n = strings.ReplaceAll(n, "__", "_")
	}
	return n
}
