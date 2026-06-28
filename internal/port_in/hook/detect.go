package hook

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// detectProject deduce el nombre del proyecto a partir del directorio de trabajo del agente.
// Prioridad (misma que usaban los scripts): nombre del repo del remoto origin > basename de la
// raíz git > basename del directorio. Devuelve "" si no hay nada que deducir.
func detectProject(cwd string) string {
	dir := strings.TrimSpace(cwd)
	if dir == "" {
		// Sin cwd explícito se consulta git en el directorio del proceso (".") — replica el
		// comportamiento del shell con `git -C ""`, que usaba el directorio actual.
		dir = "."
	}
	if name := gitRemoteName(dir); name != "" {
		return name
	}
	if root := gitTopLevel(dir); root != "" {
		return normalize(filepath.Base(root))
	}
	if strings.TrimSpace(cwd) != "" {
		return normalize(filepath.Base(cwd))
	}
	return ""
}

// oldProjectName es el nombre "anterior" del proyecto (basename del cwd) que se usa para detectar
// si el proyecto cambió de nombre y disparar la migración. Devuelve "" si no hay cwd.
func oldProjectName(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	return normalize(filepath.Base(cwd))
}

// gitRemoteName devuelve el nombre del repositorio del remoto origin (sin .git, en minúsculas), o
// "" si el directorio no tiene remoto.
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
	return normalize(url)
}

// gitTopLevel devuelve la raíz del repositorio git que contiene a dir, o "" si dir no está en uno.
func gitTopLevel(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// normalize aplica la forma canónica del nombre de proyecto: minúsculas, recortado y con
// guiones/guiones bajos consecutivos colapsados. Misma regla que internal/project y el servicio.
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
