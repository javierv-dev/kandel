package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// fakeReader sirve listas fijas de memorias (o un error) sin tocar SQLite. Registra la última
// consulta de búsqueda y los ids borrados/restaurados, y devuelve resultados/errores preprogramados.
type fakeReader struct {
	memories    []domain.Memory
	deleted     []domain.Memory // se devuelve cuando RecentQuery.OnlyDeleted (papelera)
	err         error
	searchQuery ports.SearchQuery
	results     []domain.SearchResult
	searchErr   error
	deletedID   int64
	deleteErr   error
	restoredID  int64
	restoreErr  error
}

func (f *fakeReader) Recent(_ context.Context, q ports.RecentQuery) ([]domain.Memory, error) {
	if q.OnlyDeleted {
		return f.deleted, f.err
	}
	return f.memories, f.err
}

func (f *fakeReader) Search(_ context.Context, q ports.SearchQuery) ([]domain.SearchResult, error) {
	f.searchQuery = q
	return f.results, f.searchErr
}

func (f *fakeReader) Delete(_ context.Context, id int64) error {
	f.deletedID = id
	return f.deleteErr
}

func (f *fakeReader) Restore(_ context.Context, id int64) (domain.Memory, error) {
	f.restoredID = id
	return domain.Memory{ID: id}, f.restoreErr
}

// fakeLogger registra las acciones de auditoría recibidas.
type fakeLogger struct {
	actions []loggedAction
}

type loggedAction struct {
	action string
	id     int64
	title  string
}

func (l *fakeLogger) LogAction(action string, id int64, title string) {
	l.actions = append(l.actions, loggedAction{action, id, title})
}

// sampleMemories devuelve memorias de prueba.
func sampleMemories() []domain.Memory {
	return []domain.Memory{
		{ID: 1, Type: domain.TypeDecision, Title: "Uno", Content: "contenido uno"},
		{ID: 2, Type: domain.TypeBugfix, Title: "Dos", Content: "contenido dos"},
		{ID: 3, Type: domain.TypeArchitecture, Title: "Tres", Content: "contenido tres"},
	}
}

// loaded es un modelo ya con memorias cargadas (simula el resultado de Init).
func loaded() Model {
	m := NewModel(&fakeReader{memories: sampleMemories()}, "", 20)
	next, _ := m.Update(memoriesLoadedMsg{memories: sampleMemories()})
	return next.(Model)
}

// key construye un KeyMsg a partir de un nombre de tecla.
func key(s string) tea.KeyMsg {
	switch s {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestLoadedPopulatesAndStopsLoading(t *testing.T) {
	m := loaded()
	if m.loading {
		t.Error("tras cargar, loading debe ser false")
	}
	if len(m.memories) != 3 {
		t.Errorf("memorias = %d, se esperaban 3", len(m.memories))
	}
}

func TestCursorMovesWithinBounds(t *testing.T) {
	m := loaded()
	// Baja dos veces: cursor 0→1→2.
	for i := 0; i < 2; i++ {
		next, _ := m.Update(key("down"))
		m = next.(Model)
	}
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, se esperaba 2", m.cursor)
	}
	// No pasa del último.
	next, _ := m.Update(key("down"))
	m = next.(Model)
	if m.cursor != 2 {
		t.Errorf("el cursor no debe pasar del último: %d", m.cursor)
	}
	// Sube hasta 0 y no baja de ahí.
	for i := 0; i < 5; i++ {
		next, _ := m.Update(key("up"))
		m = next.(Model)
	}
	if m.cursor != 0 {
		t.Errorf("el cursor no debe bajar de 0: %d", m.cursor)
	}
}

func TestEnterOpensDetailEscReturns(t *testing.T) {
	m := loaded()
	next, _ := m.Update(key("enter"))
	m = next.(Model)
	if m.view != viewDetail {
		t.Fatal("enter debe abrir el detalle")
	}
	if !strings.Contains(m.View(), "contenido uno") {
		t.Errorf("el detalle debe mostrar el contenido:\n%s", m.View())
	}
	next, _ = m.Update(key("esc"))
	m = next.(Model)
	if m.view != viewList {
		t.Error("esc debe volver a la lista")
	}
}

func TestQuitSetsQuitting(t *testing.T) {
	m := loaded()
	next, cmd := m.Update(key("q"))
	m = next.(Model)
	if !m.quitting {
		t.Error("q debe marcar quitting")
	}
	if cmd == nil {
		t.Error("q debe devolver el comando tea.Quit")
	}
}

func TestListViewShowsCursorMarker(t *testing.T) {
	out := loaded().listView()
	if !strings.Contains(out, "> #1") {
		t.Errorf("la primera fila debe llevar el marcador de cursor:\n%s", out)
	}
	if !strings.Contains(out, "#2") || !strings.Contains(out, "#3") {
		t.Errorf("deben listarse todas las memorias:\n%s", out)
	}
}

func TestErrorMsgShown(t *testing.T) {
	m := NewModel(&fakeReader{err: errors.New("falló la lectura")}, "", 20)
	next, _ := m.Update(errMsg{err: errors.New("falló la lectura")})
	m = next.(Model)
	if !strings.Contains(m.View(), "Error") {
		t.Errorf("la vista debe mostrar el error:\n%s", m.View())
	}
}

func TestEmptyListMessage(t *testing.T) {
	m := NewModel(&fakeReader{}, "", 20)
	next, _ := m.Update(memoriesLoadedMsg{memories: nil})
	m = next.(Model)
	if !strings.Contains(m.View(), "sin memorias") {
		t.Errorf("lista vacía debe indicarlo:\n%s", m.View())
	}
	// Enter sin memorias no debe abrir detalle.
	next, _ = m.Update(key("enter"))
	if next.(Model).view != viewList {
		t.Error("enter sin memorias no debe abrir el detalle")
	}
}

func TestSlashOpensSearch(t *testing.T) {
	m := loaded()
	next, _ := m.Update(key("/"))
	m = next.(Model)
	if !m.searching {
		t.Fatal("/ debe abrir el buscador")
	}
	if !strings.Contains(m.View(), "buscar:") {
		t.Errorf("la cabecera debe mostrar el buscador:\n%s", m.View())
	}
}

func TestSearchTypesAndRunsSemanticSearch(t *testing.T) {
	reader := &fakeReader{
		memories: sampleMemories(),
		results: []domain.SearchResult{
			{Memory: domain.Memory{ID: 9, Type: domain.TypeDecision, Title: "Resultado"}, Score: 0.9},
		},
	}
	m := NewModel(reader, "proj", 20)
	next, _ := m.Update(memoriesLoadedMsg{memories: sampleMemories()})
	m = next.(Model)

	next, _ = m.Update(key("/"))
	m = next.(Model)
	for _, r := range "login" {
		next, _ = m.Update(key(string(r)))
		m = next.(Model)
	}
	if m.query != "login" {
		t.Fatalf("query = %q, se esperaba login", m.query)
	}

	next, cmd := m.Update(key("enter"))
	m = next.(Model)
	if m.searching {
		t.Error("enter debe cerrar el buscador")
	}
	if !m.showingSearch {
		t.Error("enter debe marcar showingSearch")
	}
	if cmd == nil {
		t.Fatal("enter debe devolver el comando de búsqueda")
	}
	// Ejecuta el comando de E/S y alimenta su mensaje al modelo.
	next, _ = m.Update(cmd())
	m = next.(Model)
	if reader.searchQuery.Query != "login" || reader.searchQuery.Project != "proj" {
		t.Errorf("la consulta no llegó al reader: %+v", reader.searchQuery)
	}
	if len(m.memories) != 1 || m.memories[0].ID != 9 {
		t.Fatalf("deben verse los resultados de la búsqueda: %+v", m.memories)
	}
	if !strings.Contains(m.View(), "resultados de: login") {
		t.Errorf("se esperaba la cabecera de resultados:\n%s", m.View())
	}
}

func TestSearchEscCancels(t *testing.T) {
	m := loaded()
	next, _ := m.Update(key("/"))
	m = next.(Model)
	next, _ = m.Update(key("a"))
	m = next.(Model)
	next, _ = m.Update(key("esc"))
	m = next.(Model)
	if m.searching || m.query != "" {
		t.Errorf("esc debe cancelar la búsqueda: searching=%v query=%q", m.searching, m.query)
	}
}

func TestSearchTreatsLettersAsText(t *testing.T) {
	m := loaded()
	next, _ := m.Update(key("/"))
	m = next.(Model)
	next, cmd := m.Update(key("q"))
	m = next.(Model)
	if m.quitting || cmd != nil {
		t.Error("q dentro del buscador no debe salir")
	}
	if m.query != "q" {
		t.Errorf("q debe teclearse como texto: %q", m.query)
	}
}

func TestSearchBackspace(t *testing.T) {
	m := loaded()
	next, _ := m.Update(key("/"))
	m = next.(Model)
	for _, r := range "ab" {
		next, _ = m.Update(key(string(r)))
		m = next.(Model)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(Model)
	if m.query != "a" {
		t.Errorf("backspace debe borrar el último carácter: %q", m.query)
	}
}

func TestSearchErrorShown(t *testing.T) {
	reader := &fakeReader{memories: sampleMemories(), searchErr: errors.New("embebedor caído")}
	m := NewModel(reader, "", 20)
	next, _ := m.Update(memoriesLoadedMsg{memories: sampleMemories()})
	m = next.(Model)
	next, _ = m.Update(key("/"))
	m = next.(Model)
	next, _ = m.Update(key("x"))
	m = next.(Model)
	next, cmd := m.Update(key("enter"))
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if !strings.Contains(m.View(), "Error") {
		t.Errorf("un error de búsqueda debe mostrarse:\n%s", m.View())
	}
}

func TestDeleteAsksConfirmationThenDeletes(t *testing.T) {
	reader := &fakeReader{memories: sampleMemories()}
	m := NewModel(reader, "", 20)
	next, _ := m.Update(memoriesLoadedMsg{memories: sampleMemories()})
	m = next.(Model)

	// d pide confirmación sobre la memoria bajo el cursor (#1).
	next, _ = m.Update(key("d"))
	m = next.(Model)
	if !m.confirmingDelete {
		t.Fatal("d debe pedir confirmación")
	}
	if !strings.Contains(m.View(), "¿Borrar #1?") {
		t.Errorf("la vista debe pedir confirmación:\n%s", m.View())
	}

	// y confirma: dispara el borrado.
	next, cmd := m.Update(key("y"))
	m = next.(Model)
	if m.confirmingDelete {
		t.Error("y debe cerrar la confirmación")
	}
	if cmd == nil {
		t.Fatal("y debe devolver el comando de borrado")
	}
	// Ejecuta el borrado; su mutatedMsg debe disparar una recarga.
	next, reload := m.Update(cmd())
	m = next.(Model)
	if reader.deletedID != 1 {
		t.Errorf("debe borrarse el id 1, se borró %d", reader.deletedID)
	}
	if reload == nil {
		t.Error("tras borrar debe recargarse la lista")
	}
}

func TestDeleteConfirmationCancels(t *testing.T) {
	reader := &fakeReader{memories: sampleMemories()}
	m := NewModel(reader, "", 20)
	next, _ := m.Update(memoriesLoadedMsg{memories: sampleMemories()})
	m = next.(Model)

	next, _ = m.Update(key("d"))
	m = next.(Model)
	next, cmd := m.Update(key("n"))
	m = next.(Model)
	if m.confirmingDelete {
		t.Error("n debe cancelar la confirmación")
	}
	if cmd != nil || reader.deletedID != 0 {
		t.Errorf("n no debe borrar nada: cmd=%v deletedID=%d", cmd, reader.deletedID)
	}
}

func TestTrashToggleLoadsDeleted(t *testing.T) {
	reader := &fakeReader{
		memories: sampleMemories(),
		deleted:  []domain.Memory{{ID: 5, Type: domain.TypeBugfix, Title: "Borrada"}},
	}
	m := NewModel(reader, "", 20)
	next, _ := m.Update(memoriesLoadedMsg{memories: sampleMemories()})
	m = next.(Model)

	next, cmd := m.Update(key("t"))
	m = next.(Model)
	if !m.showingDeleted {
		t.Fatal("t debe abrir la papelera")
	}
	if cmd == nil {
		t.Fatal("t debe cargar las borradas")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if len(m.memories) != 1 || m.memories[0].ID != 5 {
		t.Fatalf("la papelera debe mostrar las borradas: %+v", m.memories)
	}
	if !strings.Contains(m.View(), "papelera") {
		t.Errorf("la cabecera debe indicar la papelera:\n%s", m.View())
	}
}

func TestRestoreFromTrash(t *testing.T) {
	reader := &fakeReader{
		memories: sampleMemories(),
		deleted:  []domain.Memory{{ID: 5, Type: domain.TypeBugfix, Title: "Borrada"}},
	}
	m := NewModel(reader, "", 20)
	next, _ := m.Update(memoriesLoadedMsg{memories: sampleMemories()})
	m = next.(Model)
	// Entrar a la papelera y cargarla.
	next, cmd := m.Update(key("t"))
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)

	// u restaura la memoria bajo el cursor.
	next, cmd = m.Update(key("u"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("u debe devolver el comando de restauración")
	}
	next, reload := m.Update(cmd())
	m = next.(Model)
	if reader.restoredID != 5 {
		t.Errorf("debe restaurarse el id 5, se restauró %d", reader.restoredID)
	}
	if reload == nil {
		t.Error("tras restaurar debe recargarse la papelera")
	}
}

func TestDeleteNotAllowedInTrash(t *testing.T) {
	reader := &fakeReader{
		memories: sampleMemories(),
		deleted:  []domain.Memory{{ID: 5, Title: "Borrada"}},
	}
	m := NewModel(reader, "", 20)
	next, _ := m.Update(memoriesLoadedMsg{memories: sampleMemories()})
	m = next.(Model)
	next, cmd := m.Update(key("t"))
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)

	next, _ = m.Update(key("d"))
	m = next.(Model)
	if m.confirmingDelete {
		t.Error("d no debe pedir confirmación en la papelera (allí se restaura)")
	}
}

func TestDeleteAuditsAction(t *testing.T) {
	reader := &fakeReader{memories: sampleMemories()}
	logger := &fakeLogger{}
	m := NewModel(reader, "", 20)
	m.logger = logger
	next, _ := m.Update(memoriesLoadedMsg{memories: sampleMemories()})
	m = next.(Model)

	next, _ = m.Update(key("d"))
	m = next.(Model)
	next, cmd := m.Update(key("y"))
	m = next.(Model)
	// Ejecuta el comando de borrado (que también audita).
	m.Update(cmd())
	if len(logger.actions) != 1 {
		t.Fatalf("se esperaba 1 acción auditada, hubo %d", len(logger.actions))
	}
	got := logger.actions[0]
	if got.action != "soft_delete" || got.id != 1 || got.title != "Uno" {
		t.Errorf("acción mal auditada: %+v", got)
	}
}

func TestRestoreAuditsAction(t *testing.T) {
	reader := &fakeReader{
		memories: sampleMemories(),
		deleted:  []domain.Memory{{ID: 5, Type: domain.TypeBugfix, Title: "Borrada"}},
	}
	logger := &fakeLogger{}
	m := NewModel(reader, "", 20)
	m.logger = logger
	next, _ := m.Update(memoriesLoadedMsg{memories: sampleMemories()})
	m = next.(Model)
	next, cmd := m.Update(key("t"))
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)

	next, cmd = m.Update(key("u"))
	m = next.(Model)
	m.Update(cmd())
	if len(logger.actions) != 1 {
		t.Fatalf("se esperaba 1 acción auditada, hubo %d", len(logger.actions))
	}
	got := logger.actions[0]
	if got.action != "restore" || got.id != 5 || got.title != "Borrada" {
		t.Errorf("acción mal auditada: %+v", got)
	}
}

func TestMutationWithoutLoggerDoesNotPanic(t *testing.T) {
	reader := &fakeReader{memories: sampleMemories()}
	m := NewModel(reader, "", 20) // sin logger (nil)
	next, _ := m.Update(memoriesLoadedMsg{memories: sampleMemories()})
	m = next.(Model)
	next, _ = m.Update(key("d"))
	m = next.(Model)
	next, cmd := m.Update(key("y"))
	m = next.(Model)
	// No debe entrar en pánico al ejecutar el comando con logger nil.
	m.Update(cmd())
	if reader.deletedID != 1 {
		t.Errorf("el borrado debe seguir funcionando sin logger: deletedID=%d", reader.deletedID)
	}
}

var _ MemoryAccess = &fakeReader{}
var _ ActionLogger = &fakeLogger{}
