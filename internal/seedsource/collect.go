// Package seedsource reúne la documentación de un proyecto en un único texto que el seed le pasa al
// LLM para que extraiga decisiones de alto valor. Es lectura pura del sistema de archivos (más un
// `git log` opcional): no toca la base de datos ni el embebedor. Aplica topes de tamaño para no
// desbordar el contexto del modelo.
package seedsource

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Límites por defecto para no desbordar el contexto del LLM. Son deliberadamente generosos pero
// acotados: el objetivo es capturar las decisiones, no el repositorio entero. Para repos grandes se
// pueden subir con las variables de entorno de abajo (o sembrar por áreas con --dir).
const (
	defaultMaxTotalBytes   = 60_000 // tope global del texto reunido
	defaultMaxPerFileBytes = 12_000 // tope por archivo individual
	defaultMaxDocFiles     = 40     // tope de archivos bajo docs/
	gitLogEntries          = "40"   // número de commits recientes a incluir
)

// Variables de entorno que ajustan los topes (útil en repos grandes).
const (
	envMaxTotalBytes   = "KANDEL_SEED_MAX_BYTES"
	envMaxPerFileBytes = "KANDEL_SEED_MAX_FILE_BYTES"
	envMaxDocFiles     = "KANDEL_SEED_MAX_DOCS"
)

// maxTotalBytes, maxPerFileBytes y maxDocFiles resuelven el tope efectivo: la variable de entorno si
// trae un entero positivo, si no el valor por defecto.
func maxTotalBytes() int   { return envInt(envMaxTotalBytes, defaultMaxTotalBytes) }
func maxPerFileBytes() int { return envInt(envMaxPerFileBytes, defaultMaxPerFileBytes) }
func maxDocFiles() int     { return envInt(envMaxDocFiles, defaultMaxDocFiles) }

// envInt lee un entero positivo de una variable de entorno; devuelve def si está ausente o es inválida.
func envInt(name string, def int) int {
	if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// rootDocNames son los archivos de la raíz que casi siempre contienen decisiones (además de cualquier
// *.md de la raíz, que también se incluye).
var manifestNames = []string{
	"go.mod", "package.json", "pyproject.toml", "Cargo.toml", "pom.xml",
	"build.gradle", "composer.json", "Gemfile", "requirements.txt",
}

// Collect reúne las fuentes del proyecto en dir y devuelve un único texto con secciones. Nunca
// falla por una fuente concreta ausente o ilegible: la omite. Devuelve error solo si dir no existe.
func Collect(ctx context.Context, dir string) (string, error) {
	if _, err := os.Stat(dir); err != nil {
		return "", err
	}

	var b strings.Builder
	budget := maxTotalBytes()

	add := func(heading, body string) {
		body = strings.TrimSpace(body)
		if body == "" || budget <= 0 {
			return
		}
		if len(body) > budget {
			body = body[:budget] + "\n…[truncado]"
		}
		section := "\n===== " + heading + " =====\n" + body + "\n"
		b.WriteString(section)
		budget -= len(section)
	}

	// 1. Archivos Markdown de la raíz (README, CLAUDE.md, AGENTS.md, PLAN/BITACORA, ADRs sueltos…).
	for _, name := range rootMarkdown(dir) {
		add("DOC raíz: "+name, readCapped(filepath.Join(dir, name)))
	}

	// 2. Manifests (revelan lenguaje, dependencias y a veces decisiones de stack).
	for _, name := range manifestNames {
		path := filepath.Join(dir, name)
		if content := readCapped(path); content != "" {
			add("MANIFEST: "+name, content)
		}
	}

	// 3. Documentación bajo docs/.
	for _, rel := range docsMarkdown(dir) {
		add("DOC: "+rel, readCapped(filepath.Join(dir, rel)))
	}

	// 4. Historial reciente de git (decisiones suelen vivir en los mensajes de commit).
	if log := gitLog(ctx, dir); log != "" {
		add("GIT LOG (reciente)", log)
	}

	// 5. Estructura de primer nivel (da contexto del layout).
	if tree := topLevelTree(dir); tree != "" {
		add("ESTRUCTURA (primer nivel)", tree)
	}

	return strings.TrimSpace(b.String()), nil
}

// rootMarkdown lista los archivos *.md (y *.markdown) directamente en la raíz, ordenados.
func rootMarkdown(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if isMarkdown(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// docsMarkdown lista, en orden, los archivos Markdown bajo docs/ (recursivo), hasta maxDocFiles.
func docsMarkdown(dir string) []string {
	docsDir := filepath.Join(dir, "docs")
	info, err := os.Stat(docsDir)
	if err != nil || !info.IsDir() {
		return nil
	}
	var rels []string
	_ = filepath.WalkDir(docsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if isMarkdown(d.Name()) {
			if rel, err := filepath.Rel(dir, path); err == nil {
				rels = append(rels, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(rels)
	if max := maxDocFiles(); len(rels) > max {
		rels = rels[:max]
	}
	return rels
}

// isMarkdown indica si el nombre tiene extensión Markdown.
func isMarkdown(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown")
}

// readCapped lee un archivo recortándolo a maxPerFileBytes. Devuelve "" si no se puede leer.
func readCapped(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if max := maxPerFileBytes(); len(data) > max {
		return string(data[:max]) + "\n…[truncado]"
	}
	return string(data)
}

// gitLog devuelve el log compacto reciente; "" si no es un repositorio git o git no está disponible.
func gitLog(ctx context.Context, dir string) string {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "log", "--oneline", "-n", gitLogEntries)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// topLevelTree lista las entradas del primer nivel del directorio (marcando los directorios con /).
func topLevelTree(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(name)
		b.WriteByte('\n')
	}
	return b.String()
}
