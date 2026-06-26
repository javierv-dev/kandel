package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/service"
)

// fakeMemory registra las llamadas recibidas para comprobar que la CLI traduce flags y
// argumentos al caso de uso correcto, sin tocar SQL ni Ollama.
type fakeMemory struct {
	saveInput   service.SaveInput
	searchQuery ports.SearchQuery
	recentQuery ports.RecentQuery
	deletedID   int64
	deletedHard bool
	getID       int64
	getResult   *domain.Memory
	saveErr     error
}

func (f *fakeMemory) Save(_ context.Context, in service.SaveInput) (service.SaveResult, error) {
	f.saveInput = in
	if f.saveErr != nil {
		return service.SaveResult{}, f.saveErr
	}
	return service.SaveResult{Memory: domain.Memory{ID: 7, Title: in.Title}, Outcome: service.SaveCreated}, nil
}

func (f *fakeMemory) Get(_ context.Context, id int64) (*domain.Memory, error) {
	f.getID = id
	return f.getResult, nil
}

func (f *fakeMemory) Recent(_ context.Context, q ports.RecentQuery) ([]domain.Memory, error) {
	f.recentQuery = q
	return nil, nil
}

func (f *fakeMemory) Search(_ context.Context, q ports.SearchQuery) ([]domain.SearchResult, error) {
	f.searchQuery = q
	return nil, nil
}

func (f *fakeMemory) Update(_ context.Context, _ int64, _ service.UpdateInput) (domain.Memory, error) {
	return domain.Memory{}, nil
}

func (f *fakeMemory) Delete(_ context.Context, id int64, hard bool) error {
	f.deletedID = id
	f.deletedHard = hard
	return nil
}

// fakeLifecycle registra pin/unpin/review.
type fakeLifecycle struct {
	pinnedID   int64
	unpinnedID int64
}

func (f *fakeLifecycle) Pin(_ context.Context, id int64) error   { f.pinnedID = id; return nil }
func (f *fakeLifecycle) Unpin(_ context.Context, id int64) error { f.unpinnedID = id; return nil }
func (f *fakeLifecycle) NeedingReview(_ context.Context, _ string, _ int) ([]domain.Memory, error) {
	return nil, nil
}

// newTestApp construye un App con dobles y buffers de salida capturables.
func newTestApp() (*App, *fakeMemory, *fakeLifecycle, *bytes.Buffer, *bytes.Buffer) {
	mem := &fakeMemory{}
	life := &fakeLifecycle{}
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	app := &App{Memory: mem, Lifecycle: life, Out: out, Err: errBuf, Version: "test"}
	return app, mem, life, out, errBuf
}

func TestDispatchUnknownCommand(t *testing.T) {
	app, _, _, _, errBuf := newTestApp()
	err := Dispatch(context.Background(), []string{"frobnicate"}, app)
	if err == nil {
		t.Fatal("se esperaba error por comando desconocido")
	}
	if !strings.Contains(errBuf.String(), "comando desconocido") {
		t.Fatalf("stderr no menciona el comando desconocido: %q", errBuf.String())
	}
}

func TestDispatchVersion(t *testing.T) {
	app, _, _, out, _ := newTestApp()
	if err := Dispatch(context.Background(), []string{"version"}, app); err != nil {
		t.Fatalf("version devolvió error: %v", err)
	}
	if !strings.Contains(out.String(), "kandel test") {
		t.Fatalf("salida de version inesperada: %q", out.String())
	}
}

func TestDispatchHelpListsRegisteredCommands(t *testing.T) {
	app, _, _, out, _ := newTestApp()
	if err := Dispatch(context.Background(), nil, app); err != nil {
		t.Fatalf("ayuda devolvió error: %v", err)
	}
	for _, name := range []string{"save", "search", "recent", "get", "delete", "pin"} {
		if !strings.Contains(out.String(), name) {
			t.Errorf("la ayuda no lista el comando %q:\n%s", name, out.String())
		}
	}
}

func TestSaveParsesFlagsAndArgs(t *testing.T) {
	app, mem, _, _, _ := newTestApp()
	args := []string{"save", "--type", "decision", "--project", "kandel", "--scope", "personal",
		"--topic", "auth/login", "Mi título", "Mi contenido"}
	if err := Dispatch(context.Background(), args, app); err != nil {
		t.Fatalf("save devolvió error: %v", err)
	}
	if mem.saveInput.Title != "Mi título" || mem.saveInput.Content != "Mi contenido" {
		t.Errorf("título/contenido mal parseados: %+v", mem.saveInput)
	}
	if mem.saveInput.Type != domain.TypeDecision {
		t.Errorf("type = %q, se esperaba decision", mem.saveInput.Type)
	}
	if mem.saveInput.Project != "kandel" {
		t.Errorf("project = %q, se esperaba kandel", mem.saveInput.Project)
	}
	if mem.saveInput.Scope != domain.ScopePersonal {
		t.Errorf("scope = %q, se esperaba personal", mem.saveInput.Scope)
	}
	if mem.saveInput.TopicKey != "auth/login" {
		t.Errorf("topic = %q, se esperaba auth/login", mem.saveInput.TopicKey)
	}
}

func TestSaveMissingArgsFails(t *testing.T) {
	app, _, _, _, _ := newTestApp()
	err := Dispatch(context.Background(), []string{"save", "solo-titulo"}, app)
	if err == nil {
		t.Fatal("se esperaba error por faltar el contenido")
	}
}

func TestSaveInvalidScopeFails(t *testing.T) {
	app, _, _, _, _ := newTestApp()
	err := Dispatch(context.Background(), []string{"save", "--scope", "raro", "t", "c"}, app)
	if err == nil {
		t.Fatal("se esperaba error por alcance inválido")
	}
}

func TestSearchParsesQueryAndLimit(t *testing.T) {
	app, mem, _, _, _ := newTestApp()
	args := []string{"search", "--limit", "3", "--type", "bugfix", "cómo arreglar el login"}
	if err := Dispatch(context.Background(), args, app); err != nil {
		t.Fatalf("search devolvió error: %v", err)
	}
	if mem.searchQuery.Query != "cómo arreglar el login" {
		t.Errorf("query = %q", mem.searchQuery.Query)
	}
	if mem.searchQuery.Limit != 3 {
		t.Errorf("limit = %d, se esperaba 3", mem.searchQuery.Limit)
	}
	if mem.searchQuery.Type != domain.TypeBugfix {
		t.Errorf("type = %q, se esperaba bugfix", mem.searchQuery.Type)
	}
}

func TestRecentDefaultLimit(t *testing.T) {
	app, mem, _, _, _ := newTestApp()
	if err := Dispatch(context.Background(), []string{"recent"}, app); err != nil {
		t.Fatalf("recent devolvió error: %v", err)
	}
	if mem.recentQuery.Limit != 20 {
		t.Errorf("limit por defecto = %d, se esperaba 20", mem.recentQuery.Limit)
	}
}

func TestDeleteHardFlag(t *testing.T) {
	app, mem, _, _, _ := newTestApp()
	if err := Dispatch(context.Background(), []string{"delete", "42", "--hard"}, app); err != nil {
		t.Fatalf("delete devolvió error: %v", err)
	}
	if mem.deletedID != 42 || !mem.deletedHard {
		t.Errorf("delete recibió id=%d hard=%v", mem.deletedID, mem.deletedHard)
	}
}

func TestDeleteInvalidIDFails(t *testing.T) {
	app, _, _, _, _ := newTestApp()
	err := Dispatch(context.Background(), []string{"delete", "no-numero"}, app)
	if err == nil {
		t.Fatal("se esperaba error por id inválido")
	}
}

func TestPinAndUnpin(t *testing.T) {
	app, _, life, _, _ := newTestApp()
	if err := Dispatch(context.Background(), []string{"pin", "5"}, app); err != nil {
		t.Fatalf("pin devolvió error: %v", err)
	}
	if life.pinnedID != 5 {
		t.Errorf("pinnedID = %d, se esperaba 5", life.pinnedID)
	}
	if err := Dispatch(context.Background(), []string{"unpin", "5"}, app); err != nil {
		t.Fatalf("unpin devolvió error: %v", err)
	}
	if life.unpinnedID != 5 {
		t.Errorf("unpinnedID = %d, se esperaba 5", life.unpinnedID)
	}
}

func TestGetNotFound(t *testing.T) {
	app, mem, _, _, _ := newTestApp()
	mem.getResult = nil
	err := Dispatch(context.Background(), []string{"get", "99"}, app)
	if err != domain.ErrNotFound {
		t.Fatalf("se esperaba ErrNotFound, se obtuvo %v", err)
	}
	if mem.getID != 99 {
		t.Errorf("getID = %d, se esperaba 99", mem.getID)
	}
}
