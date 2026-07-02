package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/javierv-dev/kandel/internal/domain"
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

// loadSearch devuelve un comando que ejecuta la búsqueda SEMÁNTICA con la consulta actual y emite
// memoriesLoadedMsg con las memorias de los resultados (descartando el score para la lista). A
// diferencia de loadRecent, este camino sí necesita el embebedor; si está caído, Search devuelve
// error y se muestra en la vista.
func (m Model) loadSearch() tea.Cmd {
	query := m.query
	return func() tea.Msg {
		results, err := m.reader.Search(context.Background(), ports.SearchQuery{
			Project: m.project,
			Query:   query,
			Limit:   m.limit,
		})
		if err != nil {
			return errMsg{err: err}
		}
		memories := make([]domain.Memory, len(results))
		for i, r := range results {
			memories[i] = r.Memory
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

// handleKey atiende la entrada de teclado según la pantalla activa. Cuando se está tecleando una
// consulta (searching) la entrada se trata como texto: solo ctrl+c sale, enter ejecuta la búsqueda
// y esc la cancela.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searching {
		return m.handleSearchInput(msg)
	}
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

	case "/":
		// Abrir el buscador semántico desde la lista.
		if m.view == viewList {
			m.searching = true
			m.query = ""
		}
		return m, nil

	case "r":
		// Recargar los recientes desde la lista (vuelve al aterrizaje, descarta la búsqueda).
		if m.view == viewList {
			m.loading = true
			m.showingSearch = false
			m.query = ""
			m.cursor = 0
			return m, m.loadRecent()
		}
		return m, nil
	}
	return m, nil
}

// handleSearchInput atiende el teclado mientras se escribe la consulta de búsqueda.
func (m Model) handleSearchInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit

	case tea.KeyEsc:
		// Cancelar la búsqueda: se mantiene la lista actual.
		m.searching = false
		m.query = ""
		return m, nil

	case tea.KeyEnter:
		if m.query == "" {
			// Consulta vacía: no busca, solo cierra el buscador.
			m.searching = false
			return m, nil
		}
		m.searching = false
		m.showingSearch = true
		m.loading = true
		m.cursor = 0
		return m, m.loadSearch()

	case tea.KeyBackspace:
		if r := []rune(m.query); len(r) > 0 {
			m.query = string(r[:len(r)-1])
		}
		return m, nil

	case tea.KeyRunes, tea.KeySpace:
		m.query += string(msg.Runes)
		return m, nil
	}
	return m, nil
}
