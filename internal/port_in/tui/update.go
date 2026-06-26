package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/javierv-dev/kandel/internal/ports"
)

// Init arranca el TUI cargando las memorias recientes.
func (m Model) Init() tea.Cmd {
	return m.loadRecent()
}

// loadRecent devuelve un comando que lee las memorias recientes y emite memoriesLoadedMsg (o
// errMsg). El efecto de E/S vive aquí, no en Update, para que Update siga siendo puro.
func (m Model) loadRecent() tea.Cmd {
	return func() tea.Msg {
		memories, err := m.reader.Recent(context.Background(), ports.RecentQuery{
			Project: m.project,
			Limit:   m.limit,
		})
		if err != nil {
			return errMsg{err: err}
		}
		return memoriesLoadedMsg{memories: memories}
	}
}

// Update transforma el modelo ante cada mensaje. Es puro: no hace E/S (eso vive en los tea.Cmd
// que devuelve) y dado el mismo (modelo, mensaje) produce el mismo resultado.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case memoriesLoadedMsg:
		m.memories = msg.memories
		m.loading = false
		if m.cursor >= len(m.memories) {
			m.cursor = 0
		}
		return m, nil

	case errMsg:
		m.err = msg.err
		m.loading = false
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey atiende la entrada de teclado según la pantalla activa.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit

	case "up", "k":
		if m.view == viewList && m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		if m.view == viewList && m.cursor < len(m.memories)-1 {
			m.cursor++
		}
		return m, nil

	case "enter":
		if m.view == viewList && len(m.memories) > 0 {
			m.view = viewDetail
		}
		return m, nil

	case "esc":
		if m.view == viewDetail {
			m.view = viewList
		}
		return m, nil

	case "r":
		// Recargar la lista desde la pantalla de lista.
		if m.view == viewList {
			m.loading = true
			return m, m.loadRecent()
		}
		return m, nil
	}
	return m, nil
}
