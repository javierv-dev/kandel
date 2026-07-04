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
		scores := make([]float64, len(results))
		for i, r := range results {
			memories[i] = r.Memory
			scores[i] = r.Score
		}
		return memoriesLoadedMsg{memories: memories, scores: scores}
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

// loadProjects devuelve un comando que carga los proyectos disponibles para filtrado.
func (m Model) loadProjects() tea.Cmd {
	return func() tea.Msg {
		projs, err := m.reader.Projects(context.Background())
		if err != nil {
			return errMsg{err: err}
		}
		return projectsLoadedMsg{projects: projs}
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

// deleteMemory devuelve un comando que borra LÓGICAMENTE la memoria dada, la registra en el log de
// auditoría (si hay logger) y emite mutatedMsg con el resultado. El título viaja para el log.
func (m Model) deleteMemory(id int64, title string) tea.Cmd {
	logger := m.logger
	return func() tea.Msg {
		err := m.reader.Delete(context.Background(), id)
		if err == nil && logger != nil {
			logger.LogAction("soft_delete", id, title)
		}
		return mutatedMsg{err: err}
	}
}

// restoreMemory devuelve un comando que restaura (deshace el borrado lógico de) la memoria dada, la
// registra en el log de auditoría (si hay logger) y emite mutatedMsg con el resultado.
func (m Model) restoreMemory(id int64, title string) tea.Cmd {
	logger := m.logger
	return func() tea.Msg {
		_, err := m.reader.Restore(context.Background(), id)
		if err == nil && logger != nil {
			logger.LogAction("restore", id, title)
		}
		return mutatedMsg{err: err}
	}
}

// selectedMemory devuelve la memoria bajo el cursor y true, o el valor cero y false si la lista
// está vacía.
func (m Model) selectedMemory() (domain.Memory, bool) {
	if m.cursor < 0 || m.cursor >= len(m.memories) {
		return domain.Memory{}, false
	}
	return m.memories[m.cursor], true
}

// selectedID devuelve el id de la memoria bajo el cursor, o 0 si la lista está vacía.
func (m Model) selectedID() int64 {
	mem, ok := m.selectedMemory()
	if !ok {
		return 0
	}
	return mem.ID
}

// Update transforma el modelo ante cada mensaje. Es puro: no hace E/S (eso vive en los tea.Cmd
// que devuelve) y dado el mismo (modelo, mensaje) produce el mismo resultado.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case memoriesLoadedMsg:
		m.memories = msg.memories
		m.scores = msg.scores
		m.loading = false
		if m.cursor >= len(m.memories) {
			m.cursor = 0
		}
		return m, nil

	case projectsLoadedMsg:
		// Insertamos la opción "[Todos]" al principio.
		m.availableProjects = append([]string{"[Todos]"}, msg.projects...)
		m.projectCursor = 0
		m.view = viewProjects
		m.loading = false
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
		} else if m.view == viewProjects && m.projectCursor > 0 {
			m.projectCursor--
		}
		return m, nil

	case "down", "j":
		if m.view == viewList && m.cursor < len(m.memories)-1 {
			m.cursor++
		} else if m.view == viewProjects && m.projectCursor < len(m.availableProjects)-1 {
			m.projectCursor++
		}
		return m, nil

	case "enter":
		if m.view == viewList && len(m.memories) > 0 {
			m.view = viewDetail
		} else if m.view == viewProjects {
			sel := m.availableProjects[m.projectCursor]
			if sel == "[Todos]" {
				m.project = ""
			} else {
				m.project = sel
			}
			m.view = viewList
			m.loading = true
			return m, m.reloadCurrent()
		}
		return m, nil

	case "esc":
		if m.view == viewDetail {
			m.view = viewList
		} else if m.view == viewProjects {
			m.view = viewList
		}
		return m, nil

	case "p":
		if m.view == viewList {
			m.loading = true
			return m, m.loadProjects()
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
		if mem, ok := m.selectedMemory(); m.view == viewList && m.showingDeleted && ok {
			m.loading = true
			return m, m.restoreMemory(mem.ID, mem.Title)
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
		mem, ok := m.selectedMemory()
		if !ok {
			return m, nil
		}
		m.loading = true
		return m, m.deleteMemory(mem.ID, mem.Title)
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
