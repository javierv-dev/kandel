package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Run arranca el programa Bubbletea con el modelo dado y bloquea hasta que el usuario sale. Es la
// frontera con el terminal real; la lógica vive en Update/View (puras y ya testeadas). logger audita
// las acciones de curación (puede ser nil para no auditar).
func Run(reader MemoryAccess, logger ActionLogger, project string, limit int) error {
	m := NewModel(reader, project, limit)
	m.logger = logger
	program := tea.NewProgram(m)
	_, err := program.Run()
	return err
}
