// Package tui es el adaptador de entrada de la interfaz de terminal (Bubbletea). Sigue el patrón
// Model/Update/View: el estado vive en Model, Update lo transforma ante cada mensaje (sin efectos
// secundarios visibles, los efectos van en tea.Cmd) y View lo dibuja. Por eso Update y View son
// funciones puras testeables sin pantalla. La lista de recientes es una lectura directa (funciona
// sin Ollama); la búsqueda es semántica y sí necesita el embebedor.
package tui

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// MemoryAccess es lo que el TUI necesita del caso de uso de memoria. Además de las lecturas
// (Recent como aterrizaje offline y la papelera vía RecentQuery.OnlyDeleted; Search semántica con
// embebedor) incluye las mutaciones de curación: Delete es SIEMPRE lógico (reversible) y Restore
// deshace ese borrado. *service.MemoryService la cumple por estructura.
type MemoryAccess interface {
	Recent(ctx context.Context, q ports.RecentQuery) ([]domain.Memory, error)
	Search(ctx context.Context, q ports.SearchQuery) ([]domain.SearchResult, error)
	Delete(ctx context.Context, id int64) error
	Restore(ctx context.Context, id int64) (domain.Memory, error)
}

// ActionLogger registra las acciones de curación de la TUI (borrar/restaurar) para auditoría. La
// interfaz la define el consumidor (la TUI); el adapter auditlog la cumple por estructura. Es
// opcional: si el modelo no tiene logger, las acciones simplemente no se auditan.
type ActionLogger interface {
	LogAction(action string, id int64, title string)
}

// view distingue las dos pantallas del TUI: la lista de memorias y el detalle de una.
type view int

const (
	viewList view = iota
	viewDetail
)

// Model es el estado completo del TUI.
type Model struct {
	reader   MemoryAccess
	project  string
	limit    int
	memories []domain.Memory
	cursor   int
	view     view
	loading  bool
	err      error
	quitting bool

	// Estado de la búsqueda semántica. searching es true mientras el usuario teclea la consulta
	// (el resto de teclas se tratan como texto). query es el texto tecleado / la última consulta
	// ejecutada. showingSearch indica que la lista actual son resultados de búsqueda (no recientes),
	// para que la cabecera lo refleje.
	searching     bool
	query         string
	showingSearch bool

	// Estado de la curación. showingDeleted indica que la lista actual es la papelera (borradas
	// lógicas); en ese modo se restaura en vez de borrar. confirmingDelete es true mientras se pide
	// la confirmación y/n antes de un borrado lógico.
	showingDeleted   bool
	confirmingDelete bool

	// logger audita las acciones de curación (opcional: nil = sin auditoría). Lo inyecta Run.
	logger ActionLogger
}

// NewModel construye el modelo inicial. El project filtra la lista (vacío = todos); limit acota
// cuántas memorias se cargan.
func NewModel(reader MemoryAccess, project string, limit int) Model {
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

// mutatedMsg señala que una mutación (borrar/restaurar) terminó. Si err no es nil, se muestra; si
// no, Update recarga la lista del modo activo para reflejar el cambio.
type mutatedMsg struct {
	err error
}
