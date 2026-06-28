package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/javierv-dev/kandel/internal/diagnostic"
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
	// El servidor expone una caja de herramientas (map nombre→tool); admin trae mem_delete y
	// mem_restore (curación: borrar lógico y deshacerlo).
	tools := srv.ListTools()
	if len(tools) != 2 || tools["mem_delete"] == nil || tools["mem_restore"] == nil {
		names := make([]string, 0, len(tools))
		for name := range tools {
			names = append(names, name)
		}
		t.Fatalf("perfil admin debe registrar mem_delete y mem_restore, registró: %v", names)
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
		t.Error("mem_get de id inexistente debe devolver error de tool")
	}
}

func TestHandleDeleteIsAlwaysSoft(t *testing.T) {
	mem := &fakeMemory{}
	svc := Services{Memory: mem}
	// Aunque la llamada pida hard=true, el agente solo puede borrar lógico (hard reservado a la CLI).
	_, err := handleDelete(svc)(context.Background(), callWith(map[string]any{"id": float64(4), "hard": true}))
	if err != nil {
		t.Fatalf("handleDelete error: %v", err)
	}
	if mem.deletedID != 4 || mem.deletedHard {
		t.Errorf("mem_delete debe ser siempre soft: id=%d hard=%v", mem.deletedID, mem.deletedHard)
	}
}

func TestHandleRestore(t *testing.T) {
	mem := &fakeMemory{}
	svc := Services{Memory: mem}
	res, err := handleRestore(svc)(context.Background(), callWith(map[string]any{"id": float64(7)}))
	if err != nil {
		t.Fatalf("handleRestore error: %v", err)
	}
	if res.IsError {
		t.Fatalf("handleRestore devolvió error de tool: %v", res)
	}
	if mem.restoredID != 7 {
		t.Errorf("restore mal traducido: id=%d", mem.restoredID)
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

func TestHandleSavePrompt(t *testing.T) {
	pr := &fakePrompt{}
	svc := Services{Prompt: pr, DefaultProject: "kandel"}
	_, err := handleSavePrompt(svc)(context.Background(), callWith(map[string]any{"content": "arregla X"}))
	if err != nil {
		t.Fatalf("handleSavePrompt error: %v", err)
	}
	if pr.last.Content != "arregla X" || pr.last.Project != "kandel" {
		t.Errorf("prompt mal traducido: %+v", pr.last)
	}
}

func TestHandleCapturePassiveExtractsAndSaves(t *testing.T) {
	mem := &fakeMemory{saveResult: service.SaveResult{Memory: domain.Memory{ID: 1}}}
	svc := Services{Memory: mem}
	content := "Resumen del trabajo.\n\n## Key Learnings:\n- FTS5 no es LIKE\n- Sanitizar siempre\n\nFin."
	res, err := handleCapturePassive(svc)(context.Background(), callWith(map[string]any{"content": content}))
	if err != nil {
		t.Fatalf("capture error: %v", err)
	}
	got := textOf(t, res)
	var parsed struct {
		Extracted int `json:"extracted"`
		Saved     int `json:"saved"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("respuesta no es JSON: %q", got)
	}
	if parsed.Extracted != 2 || parsed.Saved != 2 {
		t.Errorf("extracted/saved = %d/%d, se esperaba 2/2", parsed.Extracted, parsed.Saved)
	}
	if mem.saveCount != 2 {
		t.Errorf("Memory.Save llamado %d veces, se esperaban 2", mem.saveCount)
	}
}

func TestHandleCapturePassiveNoSection(t *testing.T) {
	svc := Services{Memory: &fakeMemory{}}
	res, _ := handleCapturePassive(svc)(context.Background(), callWith(map[string]any{"content": "sin viñetas ni encabezado"}))
	if !res.IsError {
		t.Error("sin sección de aprendizajes debe devolver error de tool")
	}
}

func TestHandleDoctor(t *testing.T) {
	svc := Services{Doctor: fakeDoctor{report: diagnostic.Report{
		Worst:    diagnostic.SeverityWarning,
		Findings: []diagnostic.Finding{{Check: "embedder", Severity: diagnostic.SeverityWarning, Message: "caído"}},
	}}}
	res, err := handleDoctor(svc)(context.Background(), callWith(nil))
	if err != nil {
		t.Fatalf("handleDoctor error: %v", err)
	}
	got := textOf(t, res)
	var parsed struct {
		OK    bool   `json:"ok"`
		Worst string `json:"worst"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("respuesta no es JSON: %q", got)
	}
	if parsed.OK || parsed.Worst != "warning" {
		t.Errorf("reporte mal serializado: %+v", parsed)
	}
}

func TestHandleCompareTranslatesIDs(t *testing.T) {
	cf := &fakeConflict{resultRel: domain.MemoryRelation{ID: 1, Kind: domain.RelationConflictsWith, Status: domain.JudgmentJudged}}
	svc := Services{Conflict: cf}
	res, err := handleCompare(svc)(context.Background(), callWith(map[string]any{"source_id": float64(3), "target_id": float64(8)}))
	if err != nil {
		t.Fatalf("handleCompare error: %v", err)
	}
	if res.IsError {
		t.Fatalf("handleCompare devolvió error de tool: %v", res)
	}
	if cf.compared != [2]int64{3, 8} {
		t.Errorf("ids mal traducidos: %v", cf.compared)
	}
}

func TestHandleCompareRequiresIDs(t *testing.T) {
	res, _ := handleCompare(Services{Conflict: &fakeConflict{}})(context.Background(), callWith(map[string]any{"source_id": float64(3)}))
	if !res.IsError {
		t.Error("mem_compare sin target_id debe dar error de tool")
	}
}

func TestHandleJudgeRecordsVerdict(t *testing.T) {
	cf := &fakeConflict{}
	svc := Services{Conflict: cf}
	_, err := handleJudge(svc)(context.Background(), callWith(map[string]any{
		"source_id": float64(1), "target_id": float64(2),
		"relation": "supersedes", "confidence": 0.8, "reasoning": "A reemplaza B",
	}))
	if err != nil {
		t.Fatalf("handleJudge error: %v", err)
	}
	if cf.recorded.Relation != domain.RelationSupersedes || cf.recorded.Confidence != 0.8 {
		t.Errorf("veredicto mal traducido: %+v", cf.recorded)
	}
}

func TestHandleJudgeInvalidRelation(t *testing.T) {
	res, _ := handleJudge(Services{Conflict: &fakeConflict{}})(context.Background(), callWith(map[string]any{
		"source_id": float64(1), "target_id": float64(2), "relation": "inventada",
	}))
	if !res.IsError {
		t.Error("relación inválida debe dar error de tool")
	}
}

func TestConflictToolsNotRegisteredWithoutPort(t *testing.T) {
	srv := NewServer(Services{Memory: &fakeMemory{}}, ResolveTools("mem_compare,mem_judge"))
	if srv.ListTools()["mem_compare"] != nil || srv.ListTools()["mem_judge"] != nil {
		t.Error("las tools de conflicto no deben registrarse sin Conflict en Services")
	}
}

func TestDoctorToolNotRegisteredWithoutPort(t *testing.T) {
	// Sin Doctor en Services, mem_doctor no debe registrarse aunque la allowlist lo permita.
	srv := NewServer(Services{Memory: &fakeMemory{}}, ResolveTools("mem_doctor"))
	if srv.ListTools()["mem_doctor"] != nil {
		t.Error("mem_doctor no debe registrarse sin Doctor en Services")
	}
}

func TestHandleCurrentProjectOverride(t *testing.T) {
	svc := Services{DefaultProject: "forzado"}
	res, err := handleCurrentProject(svc)(context.Background(), callWith(nil))
	if err != nil {
		t.Fatalf("current_project error: %v", err)
	}
	got := textOf(t, res)
	var parsed struct {
		Project string `json:"project"`
		Source  string `json:"project_source"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("respuesta no es JSON: %q", got)
	}
	if parsed.Project != "forzado" || parsed.Source != "process_override" {
		t.Errorf("el override de proceso debe ganar: %+v", parsed)
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
	saveCount   int
	getResult   *domain.Memory
	deletedID   int64
	deletedHard bool
	restoredID  int64
}

func (f *fakeMemory) Save(_ context.Context, in service.SaveInput) (service.SaveResult, error) {
	f.lastSave = in
	f.saveCount++
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
func (f *fakeMemory) Restore(_ context.Context, id int64) (domain.Memory, error) {
	f.restoredID = id
	return domain.Memory{ID: id}, nil
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

type fakePrompt struct {
	last service.SavePromptInput
}

func (f *fakePrompt) Save(_ context.Context, in service.SavePromptInput) (service.SavePromptResult, error) {
	f.last = in
	return service.SavePromptResult{Prompt: domain.Prompt{ID: 1}, Inserted: true}, nil
}

type fakeDoctor struct {
	report diagnostic.Report
}

func (f fakeDoctor) Run(context.Context) diagnostic.Report { return f.report }

type fakeConflict struct {
	compared   [2]int64
	recorded   domain.Verdict
	resultRel  domain.MemoryRelation
	compareErr error
}

func (f *fakeConflict) Compare(_ context.Context, s, t int64) (domain.MemoryRelation, error) {
	f.compared = [2]int64{s, t}
	if f.compareErr != nil {
		return domain.MemoryRelation{}, f.compareErr
	}
	return f.resultRel, nil
}
func (f *fakeConflict) RecordVerdict(_ context.Context, _, _ int64, v domain.Verdict) (domain.MemoryRelation, error) {
	f.recorded = v
	return f.resultRel, nil
}

var (
	_ MemoryPort    = (*fakeMemory)(nil)
	_ SessionPort   = (*fakeSession)(nil)
	_ LifecyclePort = (*fakeLifecycle)(nil)
	_ PromptPort    = (*fakePrompt)(nil)
	_ DoctorPort    = fakeDoctor{}
	_ ConflictPort  = (*fakeConflict)(nil)
)
