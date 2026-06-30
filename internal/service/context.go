package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// Límites de cuántos elementos entran en el contexto inyectado. Mantienen el bloque acotado para
// no inundar la ventana del agente al inicio de la sesión.
const (
	contextSessions = 5
	contextPrompts  = 10
	contextMemories = 10
)

// ContextService arma el bloque de contexto que se inyecta al agente al arrancar una sesión (o
// tras una compactación): un resumen en Markdown de sesiones, prompts y memorias recientes del
// proyecto. Es una lectura directa (navegación), no depende del embebedor.
type ContextService struct {
	sessions ports.SessionRepo
	memories ports.MemoryRepo
	prompts  ports.PromptRepo
}

// NewContextService construye el servicio con sus repos inyectados.
func NewContextService(sessions ports.SessionRepo, memories ports.MemoryRepo, prompts ports.PromptRepo) *ContextService {
	return &ContextService{sessions: sessions, memories: memories, prompts: prompts}
}

// Format devuelve el contexto del proyecto en Markdown listo para inyectar. Si no hay nada que
// mostrar devuelve cadena vacía (el hook entonces no inyecta nada).
func (c *ContextService) Format(ctx context.Context, project string, scope domain.Scope) (string, error) {
	project = normalizeProject(project)

	sessions, err := c.sessions.Recent(ctx, project, contextSessions)
	if err != nil {
		return "", err
	}
	memories, err := c.memories.Recent(ctx, ports.RecentQuery{Project: project, Scope: scope, Limit: contextMemories, ExcludeSuperseded: true})
	if err != nil {
		return "", err
	}
	prompts, err := c.prompts.Recent(ctx, project, contextPrompts)
	if err != nil {
		return "", err
	}

	if len(sessions) == 0 && len(memories) == 0 && len(prompts) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("## Memoria de sesiones anteriores\n\n")

	if len(sessions) > 0 {
		b.WriteString("### Sesiones recientes\n")
		for _, s := range sessions {
			summary := ""
			if t := strings.TrimSpace(s.Summary); t != "" {
				summary = ": " + truncate(t, 200)
			}
			fmt.Fprintf(&b, "- **%s** (%s)%s\n", s.Project, s.StartedAt.Format("2006-01-02 15:04"), summary)
		}
		b.WriteString("\n")
	}

	if len(prompts) > 0 {
		b.WriteString("### Prompts recientes del usuario\n")
		for _, p := range prompts {
			fmt.Fprintf(&b, "- %s: %s\n", p.CreatedAt.Format("2006-01-02 15:04"), truncate(p.Content, 200))
		}
		b.WriteString("\n")
	}

	if len(memories) > 0 {
		b.WriteString("### Memorias recientes\n")
		for _, m := range memories {
			fmt.Fprintf(&b, "- [%s] **%s**: %s\n", m.Type, m.Title, truncate(m.Content, 300))
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}

// truncate recorta un texto a n runas, añadiendo una elipsis si lo recortó.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
