package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// View dibuja el estado actual como texto. Es pura: solo lee el modelo. Bubbletea la llama tras
// cada Update.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.err != nil {
		return fmt.Sprintf("Error: %s\n\n(q para salir)\n", m.err)
	}
	if m.loading {
		return "Cargando memorias…\n"
	}
	if m.view == viewDetail {
		return m.detailView()
	}
	if m.view == viewProjects {
		return m.projectsView()
	}
	return m.listView()
}

// listView dibuja la lista de memorias con el cursor. La cabecera y el pie cambian según el modo:
// tecleando una consulta (buscador), mostrando resultados de búsqueda, o los recientes por defecto.
func (m Model) listView() string {
	var b strings.Builder
	projLabel := "Todos"
	if m.project != "" {
		projLabel = m.project
	}

	switch {
	case m.searching:
		fmt.Fprintf(&b, "kandel — buscar: %s▌ [Proyecto: %s]\n\n", m.query, projLabel)
	case m.showingDeleted:
		fmt.Fprintf(&b, "kandel — papelera (borradas) [Proyecto: %s]\n\n", projLabel)
	case m.showingSearch:
		fmt.Fprintf(&b, "kandel — resultados de: %s [Proyecto: %s]\n\n", m.query, projLabel)
	default:
		fmt.Fprintf(&b, "kandel — memorias recientes [Proyecto: %s]\n\n", projLabel)
	}
	if len(m.memories) == 0 {
		switch {
		case m.showingDeleted:
			b.WriteString("  (papelera vacía)\n")
		case m.showingSearch:
			b.WriteString("  (sin resultados)\n")
		default:
			b.WriteString("  (sin memorias)\n")
		}
	}
	pageSize := 10
	start := (m.cursor / pageSize) * pageSize
	end := start + pageSize
	if end > len(m.memories) {
		end = len(m.memories)
	}

	for i := start; i < end; i++ {
		mem := m.memories[i]
		marker := "  "
		if i == m.cursor {
			marker = "> "
		}
		fmt.Fprintf(&b, "%s#%d  [%s]  %s\n", marker, mem.ID, mem.Type, mem.Title)
		
		// Snippet de contexto
		snippet := extractSnippet(mem.Content, m.query, m.showingSearch)
		if snippet != "" {
			fmt.Fprintf(&b, "      %s\n", snippet)
		}

		// Indicador de similitud
		if m.showingSearch && i < len(m.scores) {
			scorePct := int(m.scores[i] * 100)
			fmt.Fprintf(&b, "      %s\n", snippetDimStyle.Render(fmt.Sprintf("└ Coincidencia: %d%%", scorePct)))
		}
	}

	if len(m.memories) > pageSize {
		totalPages := (len(m.memories) + pageSize - 1) / pageSize
		currentPage := (m.cursor / pageSize) + 1
		fmt.Fprintf(&b, "\n  --- Página %d de %d ---\n", currentPage, totalPages)
	}

	b.WriteString("\n")
	switch {
	case m.confirmingDelete:
		fmt.Fprintf(&b, "¿Borrar #%d? (y/n)\n", m.selectedID())
	case m.searching:
		b.WriteString("enter buscar · esc cancelar · ctrl+c salir\n")
	case m.showingDeleted:
		b.WriteString("↑/↓ moverse · enter ver · u restaurar · t volver · q salir\n")
	default:
		b.WriteString("↑/↓ moverse · enter ver · / buscar · p proyecto · d borrar · t papelera · r rec. · q salir\n")
	}
	return b.String()
}

// projectsView dibuja la lista de proyectos para filtrar.
func (m Model) projectsView() string {
	var b strings.Builder
	b.WriteString("kandel — seleccionar proyecto\n\n")
	
	if len(m.availableProjects) == 0 {
		b.WriteString("  (no hay proyectos)\n")
	}

	pageSize := 10
	start := (m.projectCursor / pageSize) * pageSize
	end := start + pageSize
	if end > len(m.availableProjects) {
		end = len(m.availableProjects)
	}

	for i := start; i < end; i++ {
		proj := m.availableProjects[i]
		marker := "  "
		if i == m.projectCursor {
			marker = "> "
		}
		fmt.Fprintf(&b, "%s%s\n", marker, proj)
	}

	if len(m.availableProjects) > pageSize {
		totalPages := (len(m.availableProjects) + pageSize - 1) / pageSize
		currentPage := (m.projectCursor / pageSize) + 1
		fmt.Fprintf(&b, "\n  --- Página %d de %d ---\n", currentPage, totalPages)
	}

	b.WriteString("\n↑/↓ moverse · enter seleccionar · esc cancelar\n")
	return b.String()
}

// Estilos para el snippet
var (
	snippetDimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	snippetHighlightStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("227")).Bold(true)
)

// extractSnippet genera una línea de resumen del contenido. Si estamos en modo búsqueda
// y el término aparece, lo resalta y centra el texto a su alrededor.
func extractSnippet(content, query string, isSearch bool) string {
	content = strings.ReplaceAll(content, "\n", " ")
	if !isSearch || query == "" {
		if len(content) > 70 {
			return snippetDimStyle.Render("└ " + content[:67] + "...")
		}
		return snippetDimStyle.Render("└ " + content)
	}

	lowerContent := strings.ToLower(content)
	lowerQuery := strings.ToLower(query)
	idx := strings.Index(lowerContent, lowerQuery)

	if idx == -1 {
		// Coincidencia semántica pura (la palabra no está literal)
		if len(content) > 70 {
			return snippetDimStyle.Render("└ " + content[:67] + "...")
		}
		return snippetDimStyle.Render("└ " + content)
	}

	// Si encontramos la palabra literal, extraemos un margen alrededor
	start := idx - 30
	prefix := "..."
	if start <= 0 {
		start = 0
		prefix = ""
	}
	
	end := idx + len(query) + 30
	suffix := "..."
	if end >= len(content) {
		end = len(content)
		suffix = ""
	}

	part1 := "└ " + prefix + content[start:idx]
	part2 := content[idx : idx+len(query)]
	part3 := content[idx+len(query):end] + suffix

	return snippetDimStyle.Render(part1) + snippetHighlightStyle.Render(part2) + snippetDimStyle.Render(part3)
}

// detailView dibuja el detalle de la memoria bajo el cursor.
func (m Model) detailView() string {
	if m.cursor < 0 || m.cursor >= len(m.memories) {
		return "(sin selección)\n\nesc volver · q salir\n"
	}
	mem := m.memories[m.cursor]
	var b strings.Builder
	
	fmt.Fprintf(&b, "ID: %d | Título: %s\n", mem.ID, mem.Title)
	fmt.Fprintf(&b, "Tipo: %s | Ámbito: %s\n", mem.Type, mem.Scope)
	fmt.Fprintf(&b, "Proyecto: %s\n\n", mem.Project)
	fmt.Fprintf(&b, "Detalle:\n%s\n", mem.Content)
	
	b.WriteString("\n\nesc volver · q salir\n")
	return b.String()
}
