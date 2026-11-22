package project

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Source indica de dónde salió el nombre de proyecto detectado, para que quien lo use sepa cuán
// fiable es.
type Source string

const (
	// SourceGitRemote: el nombre es el del repositorio del remoto origin (sobrevive a renombrar la
	// carpeta local). Es la fuente más estable.
	SourceGitRemote Source = "git_remote"
	// SourceGitRoot: no hay remoto; el nombre es el basename de la raíz del repositorio git (hay .git).
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

// Detect resuelve el proyecto de un directorio con prioridad, de la fuente más estable a la menos:
//  1. nombre del repositorio del remoto git origin (sobrevive a renombrar la carpeta);
//  2. basename de la raíz del repositorio git (subir hasta encontrar .git);
//  3. basename del propio directorio.
//
// Es la ÚNICA fuente de verdad de detección de proyecto: el hook y el CLI la comparten para que
// todos deriven el mismo nombre. Solo la caminata por el árbol (paso 2) no necesita el binario git;
// el paso 1 lo invoca y degrada con gracia si no está o el dir no tiene remoto.
func Detect(dir string) Detection {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return Detection{Source: SourceNone}
	}
	if name := gitRemoteName(dir); name != "" {
		return Detection{Project: Normalize(name), Source: SourceGitRemote, Path: dir}
	}
	if root, ok := gitRoot(dir); ok {
		return Detection{Project: Normalize(filepath.Base(root)), Source: SourceGitRoot, Path: root}
	}
	return Detection{Project: Normalize(filepath.Base(dir)), Source: SourceDirBasename, Path: dir}
}

// DetectCWD detecta el proyecto del directorio de trabajo actual.
func DetectCWD() Detection {
	cwd, err := os.Getwd()
	if err != nil {
		return Detection{Source: SourceNone}
	}
	return Detect(cwd)
}

// gitRemoteName devuelve el nombre del repositorio del remoto origin (sin .git, normalizado), o ""
// si no hay binario git, no hay remoto, o el directorio no es un repositorio.
func gitRemoteName(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return ""
	}
	url = strings.TrimSuffix(url, ".git")
	// El nombre es lo que va tras el último '/' o ':' (cubre URLs https y ssh).
	if i := strings.LastIndexAny(url, "/:"); i >= 0 {
		url = url[i+1:]
	}
	return url
}

// gitRoot sube por el árbol de directorios buscando una entrada .git. Devuelve el directorio que la
// contiene y true, o false si llega a la raíz del sistema sin encontrarla. Es una caminata por el
// sistema de archivos (no invoca git): funciona aunque el binario git no esté instalado.
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

// Worktree identifica una copia local por su raíz física, no por el nombre del remoto.
// En proyectos no Git se exige usar el mismo directorio raíz.
func Worktree(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("se requiere un directorio de trabajo absoluto")
	}
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(physical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("el workspace no es un directorio")
	}
	if root, ok := gitRoot(physical); ok {
		physical = root
	}
	return LocalKey("worktree", physical), nil
}

// LocalKey devuelve una clave determinista y sin separadores ambiguos.
func LocalKey(kind string, parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	return kind + "-" + hex.EncodeToString(h.Sum(nil)[:16])
}

// Normalize aplica la forma canónica del nombre de proyecto: minúsculas, recortado y con
// guiones/guiones bajos consecutivos colapsados. Es la regla única de normalización de kandel: el
// hook y el servicio la reutilizan (no la re-implementan).
func Normalize(name string) string {
	n := strings.TrimSpace(strings.ToLower(name))
	for strings.Contains(n, "--") {
		n = strings.ReplaceAll(n, "--", "-")
	}
	for strings.Contains(n, "__") {
		n = strings.ReplaceAll(n, "__", "_")
	}
	return n
}

// NormalizeList parte una cadena de proyectos separados por coma, normaliza cada elemento,
// descarta vacíos, deduplica y devuelve la lista ordenada alfabéticamente. Una entrada vacía
// (o solo comas/espacios) devuelve nil.
func NormalizeList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	var out []string
	for _, p := range parts {
		n := Normalize(p)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	if len(out) > 1 {
		for i := 0; i < len(out)-1; i++ {
			for j := i + 1; j < len(out); j++ {
				if out[j] < out[i] {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
	}
	return out
}

// NormalizeProjects devuelve la representación canónica CSV de uno o varios proyectos
// (normalizados, deduplicados y ordenados alfabéticamente, separados por coma sin espacios).
// Por ejemplo: "Yaku-Web, yaku-servidor, yaku-web" -> "yaku-servidor,yaku-web".
func NormalizeProjects(raw string) string {
	list := NormalizeList(raw)
	if len(list) == 0 {
		return ""
	}
	return strings.Join(list, ",")
}

// ContainsProject indica si target (normalizado) forma parte de la lista CSV de proyectos.
func ContainsProject(csvProjects, target string) bool {
	t := Normalize(target)
	if t == "" {
		return false
	}
	for _, p := range NormalizeList(csvProjects) {
		if p == t {
			return true
		}
	}
	return false
}

// OverlapProjects indica si dos listas CSV de proyectos comparten al menos un proyecto.
func OverlapProjects(csvA, csvB string) bool {
	listA := NormalizeList(csvA)
	listB := NormalizeList(csvB)
	if len(listA) == 0 || len(listB) == 0 {
		return false
	}
	for _, a := range listA {
		for _, b := range listB {
			if a == b {
				return true
			}
		}
	}
	return false
}
