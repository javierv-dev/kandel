// Package learnings extrae aprendizajes en viñetas de un texto que incluya una sección de
// aprendizajes ("## Key Learnings:" o "Aprendizajes:"). Es parsing de formato puro, sin reglas
// de dominio ni persistencia: lo comparten los adaptadores de entrada (MCP y HTTP) que reciben
// la salida cruda de un agente y la convierten en memorias.
package learnings

import "strings"

// Extract busca una sección de aprendizajes (encabezado tipo "Key Learnings" o "Aprendizajes")
// y devuelve el texto de las viñetas que la siguen, hasta una línea en blanco o el siguiente
// encabezado. Si no hay encabezado, devuelve nil.
func Extract(content string) []string {
	lines := strings.Split(content, "\n")
	inSection := false
	var out []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if !inSection {
			if isHeader(line) {
				inSection = true
			}
			continue
		}
		// Dentro de la sección: una línea en blanco o un nuevo encabezado la cierra.
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "#") || (strings.HasPrefix(line, "**") && strings.HasSuffix(line, ":")) {
			break
		}
		if item, ok := bulletText(line); ok {
			out = append(out, item)
		}
	}
	return out
}

// Truncate recorta un texto a n runas, añadiendo una elipsis si lo recortó. Útil para derivar el
// título de una memoria a partir de su contenido.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// isHeader indica si una línea es el encabezado de la sección de aprendizajes.
func isHeader(line string) bool {
	l := strings.ToLower(strings.Trim(line, "#* :"))
	return l == "key learnings" || l == "learnings" || l == "aprendizajes" || l == "learned"
}

// bulletText extrae el texto de una viñeta (-, *, •, +); el bool es false si la línea no es viñeta.
func bulletText(line string) (string, bool) {
	for _, prefix := range []string{"- ", "* ", "• ", "+ "} {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(line[len(prefix):]), true
		}
	}
	return "", false
}
