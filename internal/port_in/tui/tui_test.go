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

// fakeReader sirve una lista fija de memorias (o un error) sin tocar SQLite. Registra la última
// consulta de búsqueda recibida y devuelve resultados/errores preprogramados.
type fakeReader struct {
	memories    []domain.Memory
	err         error
	searchQuery ports.SearchQuery
	results     []domain.SearchResult
	searchErr   error
}

func (f *fakeReader) Recent(_ context.Context, _ ports.RecentQuery) ([]domain.Memory, error) {
	return f.memories, f.err
}

func (f *fakeReader) Search(_ context.Context, q ports.SearchQuery) ([]domain.SearchResult, error) {
	f.searchQuery = q
	return f.results, f.searchErr
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

var _ MemoryReader = &fakeReader{}
