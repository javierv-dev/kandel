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

// loadDeleted devuelve un comando que carga la papelera: las memorias borradas lógicamente
// (RecentQuery.OnlyDeleted). Es una lectura directa, no necesita el embebedor.
func (m Model) loadDeleted() tea.Cmd {
	return func() tea.Msg {
		memories, err := m.reader.Recent(context.Background(), ports.RecentQuery{
			Project:     m.project,
			Limit:       m.limit,
			OnlyDeleted: true,
		})
		if err != nil {
			return errMsg{err: err}
		}
		return memoriesLoadedMsg{memories: memories}
	}
}

// reloadCurrent devuelve el comando de carga del modo activo (papelera, resultados de búsqueda o
// recientes), para refrescar la lista tras una mutación.
func (m Model) reloadCurrent() tea.Cmd {
	switch {
	case m.showingDeleted:
		return m.loadDeleted()
	case m.showingSearch:
		return m.loadSearch()
	default:
		return m.loadRecent()
	}
}

// deleteMemory devuelve un comando que borra LÓGICAMENTE la memoria con el id dado y emite
// mutatedMsg con el resultado.
func (m Model) deleteMemory(id int64) tea.Cmd {
	return func() tea.Msg {
		return mutatedMsg{err: m.reader.Delete(context.Background(), id)}
	}
}

// restoreMemory devuelve un comando que restaura (deshace el borrado lógico de) la memoria con el
// id dado y emite mutatedMsg con el resultado.
func (m Model) restoreMemory(id int64) tea.Cmd {
	return func() tea.Msg {
		_, err := m.reader.Restore(context.Background(), id)
		return mutatedMsg{err: err}
	}
}

// selectedID devuelve el id de la memoria bajo el cursor, o 0 si la lista está vacía.
func (m Model) selectedID() int64 {
	if m.cursor < 0 || m.cursor >= len(m.memories) {
		return 0
	}
	return m.memories[m.cursor].ID
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

	case mutatedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// Mutación aplicada: recargar la lista del modo activo para reflejar el cambio.
		m.loading = true
		return m, m.reloadCurrent()

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
	if m.confirmingDelete {
		return m.handleDeleteConfirm(msg)
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
		// Abrir el buscador semántico desde la lista. No disponible en la papelera.
		if m.view == viewList && !m.showingDeleted {
			m.searching = true
			m.query = ""
		}
		return m, nil

	case "d":
		// Pedir confirmación para borrar (lógico) la memoria bajo el cursor. No en la papelera.
		if m.view == viewList && !m.showingDeleted && m.selectedID() != 0 {
			m.confirmingDelete = true
		}
		return m, nil

	case "u":
		// Restaurar la memoria bajo el cursor. Solo tiene sentido en la papelera.
		if m.view == viewList && m.showingDeleted && m.selectedID() != 0 {
			m.loading = true
			return m, m.restoreMemory(m.selectedID())
		}
		return m, nil

	case "t":
		// Alternar la papelera (borradas) ⇄ recientes desde la lista.
		if m.view == viewList {
			m.loading = true
			m.showingSearch = false
			m.query = ""
			m.cursor = 0
			m.showingDeleted = !m.showingDeleted
			return m, m.reloadCurrent()
		}
		return m, nil

	case "r":
		// Recargar los recientes desde la lista (vuelve al aterrizaje, descarta búsqueda y papelera).
		if m.view == viewList {
			m.loading = true
			m.showingSearch = false
			m.showingDeleted = false
			m.query = ""
			m.cursor = 0
			return m, m.loadRecent()
		}
		return m, nil
	}
	return m, nil
}

// handleDeleteConfirm atiende la confirmación y/n previa a un borrado lógico. y confirma; cualquier
// otra tecla (n, esc, …) cancela.
func (m Model) handleDeleteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.confirmingDelete = false
	if msg.String() == "y" {
		id := m.selectedID()
		if id == 0 {
			return m, nil
		}
		m.loading = true
		return m, m.deleteMemory(id)
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
