package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
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
	svc := Services{Memory: &fakeMemory{}, Lifecycle: &fakeLifecycle{}}
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
	svc := Services{Memory: mem}
	res, err := handleSave(svc)(context.Background(), callWith(map[string]any{
		"title": "T", "content": "C", "type": "decision", "scope": "global", "project": "kandel",
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
	if mem.lastSave.Type != domain.TypeDecision || mem.lastSave.Scope != domain.ScopeGlobal {
		t.Errorf("type/scope mal traducidos: %+v", mem.lastSave)
	}
	if mem.lastSave.Project != "kandel" {
		t.Errorf("debe pasar el proyecto explícito: %q", mem.lastSave.Project)
	}
}

// TestHandleSaveOmitsScopeLetsServiceDefault cubre F1: si no se pasa scope, el handler debe dejarlo
// vacío para que MemoryService.Save aplique DefaultScopeForType (antes ParseScope("") lo forzaba a
// project y una preference nunca quedaba global).
func TestHandleSaveOmitsScopeLetsServiceDefault(t *testing.T) {
	mem := &fakeMemory{saveResult: service.SaveResult{Memory: domain.Memory{ID: 1}, Outcome: service.SaveCreated}}
	svc := Services{Memory: mem}
	res, err := handleSave(svc)(context.Background(), callWith(map[string]any{
		"title": "T", "content": "C", "type": "preference", "project": "kandel",
	}))
	if err != nil || res.IsError {
		t.Fatalf("handleSave falló: %v / %v", err, res)
	}
	if mem.lastSave.Scope != domain.Scope("") {
		t.Errorf("sin scope explícito el handler debe pasar scope vacío (que el servicio resuelva), pasó %q", mem.lastSave.Scope)
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
		"query": "login roto", "limit": float64(3), "type": "bugfix", "project": "kandel",
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
	if mem.lastSearch.Project != "kandel" {
		t.Errorf("project mal traducido: %q", mem.lastSearch.Project)
	}
}

// La lectura es estricta: omitir el proyecto es ambiguo (no hay "proyecto del proceso").
func TestHandleSearchRequiresProject(t *testing.T) {
	mem := &fakeMemory{}
	res, _ := handleSearch(Services{Memory: mem})(context.Background(), callWith(map[string]any{"query": "x"}))
	if !res.IsError {
		t.Error("mem_search sin project debe devolver error de ambigüedad")
	}
	if mem.lastSearch.Query != "" {
		t.Error("no debe llegar a la búsqueda si el proyecto es ambiguo")
	}
}

// El comodín "*" desactiva el filtro de proyecto (busca en todos): el servicio recibe project vacío.
func TestHandleSearchWildcardMeansAllProjects(t *testing.T) {
	mem := &fakeMemory{}
	_, err := handleSearch(Services{Memory: mem})(context.Background(), callWith(map[string]any{"query": "x", "project": "*"}))
	if err != nil {
		t.Fatalf("handleSearch error: %v", err)
	}
	if mem.lastSearch.Project != "" {
		t.Errorf(`project="*" debe traducirse a filtro vacío (todos), fue %q`, mem.lastSearch.Project)
	}
}

// mem_context aplica la misma política estricta que mem_search.
func TestHandleContextRequiresProject(t *testing.T) {
	mem := &fakeMemory{}
	res, _ := handleContext(Services{Memory: mem})(context.Background(), callWith(map[string]any{}))
	if !res.IsError {
		t.Error("mem_context sin project debe devolver error de ambigüedad")
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
	// El borrado es siempre lógico: no existe borrado físico en ninguna superficie.
	_, err := handleDelete(svc)(context.Background(), callWith(map[string]any{"id": float64(4)}))
	if err != nil {
		t.Fatalf("handleDelete error: %v", err)
	}
	if mem.deletedID != 4 {
		t.Errorf("mem_delete debe borrar el id 4, borró %d", mem.deletedID)
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

func TestHandleSavePrompt(t *testing.T) {
	pr := &fakePrompt{}
	svc := Services{Prompt: pr}
	_, err := handleSavePrompt(svc)(context.Background(), callWith(map[string]any{"content": "arregla X", "project": "kandel"}))
	if err != nil {
		t.Fatalf("handleSavePrompt error: %v", err)
	}
	if pr.last.Content != "arregla X" || pr.last.Project != "kandel" {
		t.Errorf("prompt mal traducido: %+v", pr.last)
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

func TestHandleCurrentProjectDetects(t *testing.T) {
	res, err := handleCurrentProject(Services{})(context.Background(), callWith(nil))
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
	// Corre dentro del repo kandel: debe detectar un proyecto (por remoto o raíz git), nunca vacío
	// ni el desaparecido "process_override".
	if parsed.Project == "" {
		t.Errorf("debe detectar un proyecto no vacío: %+v", parsed)
	}
	switch parsed.Source {
	case "git_remote", "git_root", "dir_basename":
	default:
		t.Errorf("source de detección inesperado: %q", parsed.Source)
	}
}

// ─── mem_pending: proyecto estricto y salida agrupada ──────────────────────────

type pendingGroups struct {
	Groups []struct {
		Project  string `json:"project"`
		Memories []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"memories"`
	} `json:"groups"`
}

func parsePending(t *testing.T, res *mcp.CallToolResult) pendingGroups {
	t.Helper()
	var g pendingGroups
	if err := json.Unmarshal([]byte(textOf(t, res)), &g); err != nil {
		t.Fatalf("respuesta no es JSON de grupos: %v", err)
	}
	return g
}

func TestHandlePendingRequiresProject(t *testing.T) {
	lc := &fakeLifecycle{}
	res, _ := handlePending(Services{Lifecycle: lc})(context.Background(), callWith(nil))
	if !res.IsError {
		t.Fatal("mem_pending sin proyecto debe devolver error de tool")
	}
	if len(lc.calls) != 0 {
		t.Errorf("no debe consultar el repo sin proyecto, consultó: %v", lc.calls)
	}
}

func TestHandlePendingSingleProject(t *testing.T) {
	lc := &fakeLifecycle{byProject: map[string][]domain.Memory{
		"kandel": {{ID: 1, Title: "a"}},
	}}
	res, err := handlePending(Services{Lifecycle: lc})(context.Background(), callWith(map[string]any{"project": "kandel"}))
	if err != nil || res.IsError {
		t.Fatalf("handlePending falló: %v / %v", err, res)
	}
	g := parsePending(t, res)
	if len(g.Groups) != 1 || g.Groups[0].Project != "kandel" || len(g.Groups[0].Memories) != 1 {
		t.Fatalf("se esperaba un grupo 'kandel' con 1 memoria: %+v", g)
	}
	if want := []string{"kandel"}; !reflect.DeepEqual(lc.calls, want) {
		t.Errorf("calls = %v, se esperaba %v", lc.calls, want)
	}
}

func TestHandlePendingMultipleProjectsGrouped(t *testing.T) {
	lc := &fakeLifecycle{byProject: map[string][]domain.Memory{
		"a": {{ID: 1, Title: "x"}},
		"b": {{ID: 2, Title: "y"}, {ID: 3, Title: "z"}},
	}}
	res, err := handlePending(Services{Lifecycle: lc})(context.Background(), callWith(map[string]any{
		"projects": []any{"a", "b"},
	}))
	if err != nil || res.IsError {
		t.Fatalf("handlePending falló: %v / %v", err, res)
	}
	g := parsePending(t, res)
	if len(g.Groups) != 2 {
		t.Fatalf("se esperaban 2 grupos separados: %+v", g)
	}
	if g.Groups[0].Project != "a" || len(g.Groups[0].Memories) != 1 {
		t.Errorf("grupo 0 mal: %+v", g.Groups[0])
	}
	if g.Groups[1].Project != "b" || len(g.Groups[1].Memories) != 2 {
		t.Errorf("grupo 1 mal: %+v", g.Groups[1])
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(lc.calls, want) {
		t.Errorf("calls = %v, se esperaba una consulta por proyecto %v", lc.calls, want)
	}
}

func TestHandlePendingAllProjectsGroupsByProject(t *testing.T) {
	lc := &fakeLifecycle{byProject: map[string][]domain.Memory{
		"": {{ID: 1, Title: "x", Project: "a"}, {ID: 2, Title: "y", Project: "b"}, {ID: 3, Title: "z", Project: "a"}},
	}}
	res, err := handlePending(Services{Lifecycle: lc})(context.Background(), callWith(map[string]any{"project": "*"}))
	if err != nil || res.IsError {
		t.Fatalf("handlePending falló: %v / %v", err, res)
	}
	if want := []string{""}; !reflect.DeepEqual(lc.calls, want) {
		t.Errorf(`"*" debe consultar una vez sin filtro, calls = %v`, lc.calls)
	}
	g := parsePending(t, res)
	if len(g.Groups) != 2 || g.Groups[0].Project != "a" || g.Groups[1].Project != "b" {
		t.Fatalf("se esperaban grupos a,b por proyecto de cada memoria: %+v", g)
	}
	if len(g.Groups[0].Memories) != 2 || len(g.Groups[1].Memories) != 1 {
		t.Errorf("reparto por proyecto incorrecto: %+v", g)
	}
}

// syncConfigured fija env de sync válido y aísla el directorio de datos (para no leer el
// ~/.kandel/config real ni escribir en él).
func syncConfigured(t *testing.T) {
	t.Helper()
	t.Setenv("KANDEL_DATA_DIR", t.TempDir())
	t.Setenv("KANDEL_SYNC_URL", "http://localhost:8766")
	t.Setenv("KANDEL_SYNC_TOKEN", "tok")
}

func TestHandleSyncPushPull(t *testing.T) {
	syncConfigured(t)
	sy := &fakeSync{pushResult: service.PushResult{Count: 2}, pullResult: service.PullResult{Applied: 3}}
	res, err := handleSync(Services{Sync: sy})(context.Background(), callWith(nil))
	if err != nil {
		t.Fatalf("handleSync error: %v", err)
	}
	if res.IsError {
		t.Fatalf("handleSync devolvió error de tool: %s", textOf(t, res))
	}
	if !sy.pushed || !sy.pulled {
		t.Errorf("mem_sync debe hacer push Y pull: pushed=%v pulled=%v", sy.pushed, sy.pulled)
	}
	var parsed struct {
		Pushed    int `json:"pushed"`
		Pulled    int `json:"pulled"`
		Conflicts int `json:"conflicts"`
	}
	if err := json.Unmarshal([]byte(textOf(t, res)), &parsed); err != nil {
		t.Fatalf("respuesta no es JSON: %v", err)
	}
	if parsed.Pushed != 2 || parsed.Pulled != 3 || parsed.Conflicts != 0 {
		t.Errorf("resumen mal serializado: %+v", parsed)
	}
}

func TestHandleSyncNotConfigured(t *testing.T) {
	t.Setenv("KANDEL_DATA_DIR", t.TempDir())
	t.Setenv("KANDEL_SYNC_URL", "")
	t.Setenv("KANDEL_SYNC_TOKEN", "")
	sy := &fakeSync{}
	res, _ := handleSync(Services{Sync: sy})(context.Background(), callWith(nil))
	if !res.IsError {
		t.Fatal("mem_sync sin sync configurado debe dar error de tool")
	}
	if sy.pushed || sy.pulled {
		t.Error("sin configurar no debe tocar el sync service")
	}
}

func TestHandleSyncReportsConflicts(t *testing.T) {
	syncConfigured(t)
	sy := &fakeSync{
		pullResult: service.PullResult{Applied: 1, Conflicts: 1},
		conflicts: []domain.SyncConflict{{
			SyncID: "mem-x", Project: "kandel", IncomingOp: domain.SyncOpUpsert,
			Incoming: mustJSONMem(domain.Memory{Title: "remota"}),
		}},
	}
	res, _ := handleSync(Services{Sync: sy})(context.Background(), callWith(nil))
	got := textOf(t, res)
	if !strings.Contains(got, "conflict_note") || !strings.Contains(got, "mem-x") || !strings.Contains(got, "remota") {
		t.Errorf("mem_sync con conflicto debe adjuntar la lista y el aviso: %s", got)
	}
}

func TestSyncToolNotRegisteredWithoutPort(t *testing.T) {
	srv := NewServer(Services{Memory: &fakeMemory{}}, ResolveTools("mem_sync"))
	if srv.ListTools()["mem_sync"] != nil {
		t.Error("mem_sync no debe registrarse sin Sync en Services")
	}
}

func mustJSONMem(m domain.Memory) []byte {
	b, _ := json.Marshal(m)
	return b
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
	lastSave   service.SaveInput
	lastSearch ports.SearchQuery
	saveResult service.SaveResult
	saveCount  int
	getResult  *domain.Memory
	deletedID  int64
	restoredID int64
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
func (f *fakeMemory) Delete(_ context.Context, id int64) error {
	f.deletedID = id
	return nil
}
func (f *fakeMemory) Restore(_ context.Context, id int64) (domain.Memory, error) {
	f.restoredID = id
	return domain.Memory{ID: id}, nil
}

type fakeLifecycle struct {
	byProject map[string][]domain.Memory // respuesta por proyecto ("" = todos)
	calls     []string                   // proyectos consultados, en orden
}

func (f *fakeLifecycle) PendingResolution(_ context.Context, project string, _ int) ([]domain.Memory, error) {
	f.calls = append(f.calls, project)
	if f.byProject == nil {
		return nil, nil
	}
	return f.byProject[project], nil
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

type fakeSync struct {
	pushResult service.PushResult
	pullResult service.PullResult
	conflicts  []domain.SyncConflict
	pushed     bool
	pulled     bool
	pushErr    error
}

func (f *fakeSync) Push(context.Context) (service.PushResult, error) {
	f.pushed = true
	return f.pushResult, f.pushErr
}
func (f *fakeSync) Pull(context.Context) (service.PullResult, error) {
	f.pulled = true
	return f.pullResult, nil
}
func (f *fakeSync) ListConflicts(context.Context) ([]domain.SyncConflict, error) {
	return f.conflicts, nil
}

var (
	_ MemoryPort    = (*fakeMemory)(nil)
	_ LifecyclePort = (*fakeLifecycle)(nil)
	_ PromptPort    = (*fakePrompt)(nil)
	_ DoctorPort    = fakeDoctor{}
	_ ConflictPort  = (*fakeConflict)(nil)
	_ SyncPort      = (*fakeSync)(nil)
)
