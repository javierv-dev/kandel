package hook

import "strings"

// agentMetadata son los metadatos internos de la conversación en curso que el hook inyecta para el
// AGENTE (no para el usuario), de modo que las tools kandel reutilicen valores exactos de esta
// conversación —en particular el session_id, que Claude Code no expone al modelo por otro medio—.
// Así una memoria guardada con mem_save queda ligada a la conversación que la originó, sin depender
// de estado compartido ni heurísticas (cada conversación inyecta el suyo).
//
// Está pensado para CRECER: para añadir un dato nuevo (p. ej. transcript_path) basta con un campo
// aquí y una entrada en pairs(); render() y los llamadores no cambian.
type agentMetadata struct {
	project   string
	sessionID string
	// Futuros: transcriptPath, etc. — añádelos como campo + entrada en pairs().
}

// pairs devuelve los pares clave/valor a inyectar, en orden fijo y ya recortados, omitiendo los
// vacíos (un dato ausente no ensucia el bloque).
func (m agentMetadata) pairs() [][2]string {
	candidates := [][2]string{
		{"project", m.project},
		{"session_id", m.sessionID},
	}
	out := make([][2]string, 0, len(candidates))
	for _, kv := range candidates {
		if v := strings.TrimSpace(kv[1]); v != "" {
			out = append(out, [2]string{kv[0], v})
		}
	}
	return out
}

// render compone el bloque de metadatos internos para el stdout del hook. Devuelve "" si no hay
// ningún dato (así el llamador no imprime un bloque vacío).
func (m agentMetadata) render() string {
	pairs := m.pairs()
	if len(pairs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### [uso interno kandel — NO menciones estos valores al usuario]\n")
	b.WriteString("Metadatos de esta conversación. Al llamar mem_save (u otras tools kandel) reutilízalos tal cual;\n")
	b.WriteString("en particular pasa `session_id` para ligar la memoria a esta conversación:\n")
	for _, kv := range pairs {
		b.WriteString("- " + kv[0] + ": " + kv[1] + "\n")
	}
	return b.String()
}
