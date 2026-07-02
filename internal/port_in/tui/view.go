package tui

import (
	"fmt"
	"strings"
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
	return m.listView()
}

// listView dibuja la lista de memorias con el cursor. La cabecera y el pie cambian según el modo:
// tecleando una consulta (buscador), mostrando resultados de búsqueda, o los recientes por defecto.
func (m Model) listView() string {
	var b strings.Builder
	switch {
	case m.searching:
		fmt.Fprintf(&b, "kandel — buscar: %s▌\n\n", m.query)
	case m.showingSearch:
		fmt.Fprintf(&b, "kandel — resultados de: %s\n\n", m.query)
	default:
		b.WriteString("kandel — memorias recientes\n\n")
	}
	if len(m.memories) == 0 {
		if m.showingSearch {
			b.WriteString("  (sin resultados)\n")
		} else {
			b.WriteString("  (sin memorias)\n")
		}
	}
	for i, mem := range m.memories {
		marker := "  "
		if i == m.cursor {
			marker = "> "
		}
		fmt.Fprintf(&b, "%s#%d  [%s]  %s\n", marker, mem.ID, mem.Type, mem.Title)
	}
	if m.searching {
		b.WriteString("\nenter buscar · esc cancelar · ctrl+c salir\n")
	} else {
		b.WriteString("\n↑/↓ moverse · enter ver · / buscar · r recientes · q salir\n")
	}
	return b.String()
}

// detailView dibuja el detalle de la memoria bajo el cursor.
func (m Model) detailView() string {
	if m.cursor < 0 || m.cursor >= len(m.memories) {
		return "(sin selección)\n\nesc volver · q salir\n"
	}
	mem := m.memories[m.cursor]
	var b strings.Builder
	fmt.Fprintf(&b, "#%d  [%s/%s]  %s\n", mem.ID, mem.Type, mem.Scope, mem.Title)
	fmt.Fprintf(&b, "proyecto: %s\n\n%s\n", mem.Project, mem.Content)
	b.WriteString("\nesc volver · q salir\n")
	return b.String()
}
