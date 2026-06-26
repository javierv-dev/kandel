// Package tui es el adaptador de entrada de la interfaz de terminal (Bubbletea). Sigue el patrón
// Model/Update/View: el estado vive en Model, Update lo transforma ante cada mensaje (sin efectos
// secundarios visibles, los efectos van en tea.Cmd) y View lo dibuja. Por eso Update y View son
// funciones puras testeables sin pantalla. Solo consume lecturas directas de service/ (navegación
// del usuario): no depende del embebedor.
package tui

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// MemoryReader es lo que el TUI necesita del caso de uso de memoria: listar recientes. Es una
// lectura directa, así que la interfaz se mantiene mínima (no incluye búsqueda semántica, que
// dependería del embebedor).
type MemoryReader interface {
	Recent(ctx context.Context, q ports.RecentQuery) ([]domain.Memory, error)
}

// view distingue las dos pantallas del TUI: la lista de memorias y el detalle de una.
type view int

const (
	viewList view = iota
	viewDetail
)

// Model es el estado completo del TUI.
type Model struct {
	reader   MemoryReader
	project  string
	limit    int
	memories []domain.Memory
	cursor   int
	view     view
	loading  bool
	err      error
	quitting bool
}

// NewModel construye el modelo inicial. El project filtra la lista (vacío = todos); limit acota
// cuántas memorias se cargan.
func NewModel(reader MemoryReader, project string, limit int) Model {
	if limit <= 0 {
		limit = 20
	}
	return Model{
		reader:  reader,
		project: project,
		limit:   limit,
		view:    viewList,
		loading: true,
	}
}

// ─── mensajes ──────────────────────────────────────────────────────────────────

// memoriesLoadedMsg lleva las memorias cargadas por el comando de carga.
type memoriesLoadedMsg struct {
	memories []domain.Memory
}

// errMsg lleva un error ocurrido en un comando.
type errMsg struct {
	err error
}
