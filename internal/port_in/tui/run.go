package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Run arranca el programa Bubbletea con el modelo dado y bloquea hasta que el usuario sale. Es la
// frontera con el terminal real; la lógica vive en Update/View (puras y ya testeadas).
func Run(reader MemoryReader, project string, limit int) error {
	program := tea.NewProgram(NewModel(reader, project, limit))
	_, err := program.Run()
	return err
}
