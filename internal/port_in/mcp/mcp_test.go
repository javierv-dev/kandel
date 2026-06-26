package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

// ─── ResolveTools / allowlist ──────────────────────────────────────────────────

func TestResolveToolsEmptyMeansAll(t *testing.T) {
	if ResolveTools("") != nil {
		t.Error("entrada vacía debe devolver nil (todas)")
	}
	if ResolveTools("all") != nil {
		t.Error("\"all\" debe devolver nil (todas)")
	}
}

func TestResolveToolsProfileExpands(t *testing.T) {
	got := ResolveTools("admin")
	if !got["mem_delete"] {
		t.Errorf("perfil admin debe incluir mem_delete: %v", got)
	}
	if got["mem_save"] {
		t.Errorf("perfil admin no debe incluir mem_save: %v", got)
	}
}

func TestResolveToolsIndividualAndCombined(t *testing.T) {
	got := ResolveTools("agent,mem_delete")
	if !got["mem_save"] || !got["mem_delete"] {
		t.Errorf("debe combinar perfil agent + tool suelta: %v", got)
	}
}

func TestShouldRegisterNilAllowsAll(t *testing.T) {
	if !shouldRegister("mem_delete", nil) {
		t.Error("allowlist nil debe permitir todo")
	}
	if shouldRegister("mem_delete", map[string]bool{"mem_save": true}) {
		t.Error("herramienta fuera de la allowlist no debe registrarse")
	}
}

// ─── Servidor: la allowlist filtra el registro ─────────────────────────────────

func TestNewServerRegistersOnlyAllowedTools(t *testing.T) {
	svc := Services{Memory: &fakeMemory{}, Session: &fakeSession{}, Lifecycle: &fakeLifecycle{}}
	srv := NewServer(svc, ResolveTools("admin"))
	// El servidor expone una caja de herramientas (map nombre→tool); admin solo trae mem_delete.
	tools := srv.ListTools()
	if len(tools) != 1 || tools["mem_delete"] == nil {
		names := make([]string, 0, len(tools))
		for name := range tools {
			names = append(names, name)
		}
		t.Fatalf("perfil admin debe registrar solo mem_delete, registró: %v", names)
	}
}

// ─── Handlers: traducción de argumentos a casos de uso ─────────────────────────

func TestHandleSaveTranslatesArgs(t *testing.T) {
	mem := &fakeMemory{saveResult: service.SaveResult{Memory: domain.Memory{ID: 5, Title: "T"}, Outcome: service.SaveCreated}}
	svc := Services{Memory: mem, DefaultProject: "kandel"}
	res, err := handleSave(svc)(context.Background(), callWith(map[string]any{
		"title": "T", "content": "C", "type": "decision", "scope": "personal", "topic_key": "auth/login",
	}))
	if err != nil {
		t.Fatalf("handleSave error: %v", err)
	}
	if res.IsError {
		t.Fatalf("handleSave devolvió error de tool: %v", res)
	}
	if mem.lastSave.Title != "T" || mem.lastSave.Content != "C" {
		t.Errorf("título/contenido mal traducidos: %+v", mem.lastSave)
	}
	if mem.lastSave.Type != domain.TypeDecision || mem.lastSave.Scope != domain.ScopePersonal {
		t.Errorf("type/scope mal traducidos: %+v", mem.lastSave)
	}
	if mem.lastSave.Project != "kandel" {
		t.Errorf("debe usar el proyecto del proceso: %q", mem.lastSave.Project)
	}
	if mem.lastSave.TopicKey != "auth/login" {
		t.Errorf("topic_key mal traducido: %q", mem.lastSave.TopicKey)
	}
}

func TestHandleSaveRequiresContent(t *testing.T) {
	svc := Services{Memory: &fakeMemory{}}
	res, _ := handleSave(svc)(context.Background(), callWith(map[string]any{"title": "T"}))
	if !res.IsError {
		t.Error("mem_save sin content debe devolver error de tool")
	}
}

func TestHandleSearchPassesQueryAndLimit(t *testing.T) {
	mem := &fakeMemory{}
	svc := Services{Memory: mem}
	_, err := handleSearch(svc)(context.Background(), callWith(map[string]any{
		"query": "login roto", "limit": float64(3), "type": "bugfix",
	}))
	if err != nil {
		t.Fatalf("handleSearch error: %v", err)
	}
	if mem.lastSearch.Query != "login roto" || mem.lastSearch.Limit != 3 {
		t.Errorf("query/limit mal traducidos: %+v", mem.lastSearch)
	}
	if mem.lastSearch.Type != domain.TypeBugfix {
		t.Errorf("type mal traducido: %q", mem.lastSearch.Type)
	}
}

func TestHandleGetNotFound(t *testing.T) {
	svc := Services{Memory: &fakeMemory{getResult: nil}}
	res, _ := handleGet(svc)(context.Background(), callWith(map[string]any{"id": float64(9)}))
	if !res.IsError {
		t.Error("mem_get_observation de id inexistente debe devolver error de tool")
	}
}

func TestHandleDeleteHard(t *testing.T) {
	mem := &fakeMemory{}
	svc := Services{Memory: mem}
	_, err := handleDelete(svc)(context.Background(), callWith(map[string]any{"id": float64(4), "hard": true}))
	if err != nil {
		t.Fatalf("handleDelete error: %v", err)
	}
	if mem.deletedID != 4 || !mem.deletedHard {
		t.Errorf("delete mal traducido: id=%d hard=%v", mem.deletedID, mem.deletedHard)
	}
}

func TestHandlePin(t *testing.T) {
	life := &fakeLifecycle{}
	svc := Services{Lifecycle: life}
	if _, err := handlePin(svc, true)(context.Background(), callWith(map[string]any{"id": float64(7)})); err != nil {
		t.Fatalf("handlePin error: %v", err)
	}
	if life.pinnedID != 7 {
		t.Errorf("pinnedID = %d, se esperaba 7", life.pinnedID)
	}
}

func TestHandleSessionSummaryRequiresSummary(t *testing.T) {
	svc := Services{Session: &fakeSession{}}
	res, _ := handleSessionEnd(svc, true)(context.Background(), callWith(map[string]any{"id": "s1"}))
	if !res.IsError {
		t.Error("mem_session_summary sin summary debe devolver error de tool")
	}
}

func TestHandleSuggestTopicKey(t *testing.T) {
	res, err := handleSuggestTopicKey()(context.Background(), callWith(map[string]any{
		"type": "Architecture", "title": "Auth Model V2",
	}))
	if err != nil {
		t.Fatalf("suggest error: %v", err)
	}
	got := textOf(t, res)
	var parsed struct {
		TopicKey string `json:"topic_key"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("respuesta no es JSON: %q", got)
	}
	if parsed.TopicKey != "architecture/auth-model-v2" {
		t.Errorf("topic_key = %q, se esperaba architecture/auth-model-v2", parsed.TopicKey)
	}
}

// ─── utilidades de test ────────────────────────────────────────────────────────

// callWith construye una CallToolRequest con los argumentos dados.
func callWith(args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	return req
}

// textOf extrae el texto del primer contenido del resultado.
func textOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("el resultado no tiene contenido")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("el contenido no es texto: %T", res.Content[0])
	}
	return tc.Text
}

// ─── dobles ────────────────────────────────────────────────────────────────────

type fakeMemory struct {
	lastSave    service.SaveInput
	lastSearch  ports.SearchQuery
	saveResult  service.SaveResult
	getResult   *domain.Memory
	deletedID   int64
	deletedHard bool
}

func (f *fakeMemory) Save(_ context.Context, in service.SaveInput) (service.SaveResult, error) {
	f.lastSave = in
	return f.saveResult, nil
}
func (f *fakeMemory) Get(_ context.Context, _ int64) (*domain.Memory, error) { return f.getResult, nil }
func (f *fakeMemory) Recent(_ context.Context, _ ports.RecentQuery) ([]domain.Memory, error) {
	return nil, nil
}
func (f *fakeMemory) Search(_ context.Context, q ports.SearchQuery) ([]domain.SearchResult, error) {
	f.lastSearch = q
	return nil, nil
}
func (f *fakeMemory) Update(_ context.Context, id int64, _ service.UpdateInput) (domain.Memory, error) {
	return domain.Memory{ID: id}, nil
}
func (f *fakeMemory) Delete(_ context.Context, id int64, hard bool) error {
	f.deletedID = id
	f.deletedHard = hard
	return nil
}

type fakeSession struct {
	endedID string
	summary string
}

func (f *fakeSession) Start(_ context.Context, in service.StartInput) (domain.Session, error) {
	return domain.Session{ID: in.ID, Project: in.Project}, nil
}
func (f *fakeSession) End(_ context.Context, id, summary string) error {
	f.endedID = id
	f.summary = summary
	return nil
}
func (f *fakeSession) Get(_ context.Context, _ string) (*domain.Session, error) { return nil, nil }
func (f *fakeSession) MostRecentActive(_ context.Context, _ string) (string, bool, error) {
	return "", false, nil
}

type fakeLifecycle struct {
	pinnedID   int64
	unpinnedID int64
}

func (f *fakeLifecycle) Pin(_ context.Context, id int64) error   { f.pinnedID = id; return nil }
func (f *fakeLifecycle) Unpin(_ context.Context, id int64) error { f.unpinnedID = id; return nil }
func (f *fakeLifecycle) NeedingReview(_ context.Context, _ string, _ int) ([]domain.Memory, error) {
	return nil, nil
}

var (
	_ MemoryPort    = (*fakeMemory)(nil)
	_ SessionPort   = (*fakeSession)(nil)
	_ LifecyclePort = (*fakeLifecycle)(nil)
)
