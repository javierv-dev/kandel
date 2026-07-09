package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/service"
)

// do envía una petición al handler y devuelve la respuesta grabada.
func do(t *testing.T, svc Services, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, rdr)
	rec := httptest.NewRecorder()
	NewHandler(svc).ServeHTTP(rec, req)
	return rec
}

func TestHealth(t *testing.T) {
	rec := do(t, Services{}, http.MethodGet, "/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, se esperaba 200", rec.Code)
	}
}

func TestSaveMemory(t *testing.T) {
	mem := &fakeMemory{saveResult: service.SaveResult{Memory: domain.Memory{ID: 9, Title: "T"}, Outcome: service.SaveCreated}}
	rec := do(t, Services{Memory: mem}, http.MethodPost, "/memories",
		`{"title":"T","content":"C","type":"decision","scope":"global","project":"kandel"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, se esperaba 201; body=%s", rec.Code, rec.Body.String())
	}
	if mem.lastSave.Title != "T" || mem.lastSave.Type != domain.TypeDecision || mem.lastSave.Scope != domain.ScopeGlobal {
		t.Errorf("cuerpo mal traducido: %+v", mem.lastSave)
	}
	if mem.lastSave.Project != "kandel" {
		t.Errorf("project mal traducido: %+v", mem.lastSave)
	}
}

func TestSaveMemoryInvalidScope(t *testing.T) {
	rec := do(t, Services{Memory: &fakeMemory{}}, http.MethodPost, "/memories", `{"title":"T","content":"C","scope":"raro"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("scope inválido debe dar 400, dio %d", rec.Code)
	}
}

func TestSaveMemoryBadJSON(t *testing.T) {
	rec := do(t, Services{Memory: &fakeMemory{}}, http.MethodPost, "/memories", `{no es json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("JSON inválido debe dar 400, dio %d", rec.Code)
	}
}

func TestGetMemoryNotFound(t *testing.T) {
	rec := do(t, Services{Memory: &fakeMemory{getResult: nil}}, http.MethodGet, "/memories/7", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, se esperaba 404", rec.Code)
	}
}

func TestGetMemoryBadID(t *testing.T) {
	rec := do(t, Services{Memory: &fakeMemory{}}, http.MethodGet, "/memories/abc", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("id no numérico debe dar 400, dio %d", rec.Code)
	}
}

func TestDeleteMemory(t *testing.T) {
	mem := &fakeMemory{}
	rec := do(t, Services{Memory: mem}, http.MethodDelete, "/memories/4", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, se esperaba 204", rec.Code)
	}
	if mem.deletedID != 4 {
		t.Errorf("delete mal traducido: id=%d", mem.deletedID)
	}
}

func TestSearch(t *testing.T) {
	mem := &fakeMemory{}
	rec := do(t, Services{Memory: mem}, http.MethodGet, "/search?q=login+roto&limit=3&type=bugfix", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, se esperaba 200", rec.Code)
	}
	if mem.lastSearch.Query != "login roto" || mem.lastSearch.Limit != 3 || mem.lastSearch.Type != domain.TypeBugfix {
		t.Errorf("query mal traducida: %+v", mem.lastSearch)
	}
}

func TestSearchMissingQuery(t *testing.T) {
	rec := do(t, Services{Memory: &fakeMemory{}}, http.MethodGet, "/search", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("sin q debe dar 400, dio %d", rec.Code)
	}
}

func TestUpdateMemoryPartial(t *testing.T) {
	mem := &fakeMemory{updateResult: domain.Memory{ID: 2, Title: "nuevo"}}
	rec := do(t, Services{Memory: mem}, http.MethodPatch, "/memories/2", `{"title":"nuevo"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, se esperaba 200; body=%s", rec.Code, rec.Body.String())
	}
	if mem.updateInput.Title == nil || *mem.updateInput.Title != "nuevo" {
		t.Errorf("title no llegó al update: %+v", mem.updateInput)
	}
	if mem.updateInput.Content != nil {
		t.Errorf("content no enviado no debe tocarse: %+v", mem.updateInput)
	}
}

func TestStartSession(t *testing.T) {
	sess := &fakeSession{}
	rec := do(t, Services{Session: sess}, http.MethodPost, "/sessions", `{"id":"s1","project":"kandel"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, se esperaba 201; body=%s", rec.Code, rec.Body.String())
	}
	if sess.started.ID != "s1" {
		t.Errorf("id de sesión no llegó: %+v", sess.started)
	}
}

func TestStartSessionMissingID(t *testing.T) {
	rec := do(t, Services{Session: &fakeSession{}}, http.MethodPost, "/sessions", `{"project":"kandel"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("sin id debe dar 400, dio %d", rec.Code)
	}
}

func TestEndSessionNotFound(t *testing.T) {
	sess := &fakeSession{endErr: domain.ErrNotFound}
	rec := do(t, Services{Session: sess}, http.MethodPost, "/sessions/x/end", ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, se esperaba 404", rec.Code)
	}
}

func TestSavePrompt(t *testing.T) {
	pr := &fakePrompt{}
	rec := do(t, Services{Prompt: pr}, http.MethodPost, "/prompts", `{"content":"haz X","project":"kandel"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, se esperaba 201; body=%s", rec.Code, rec.Body.String())
	}
	if pr.last.Content != "haz X" {
		t.Errorf("prompt no llegó: %+v", pr.last)
	}
}

func TestContext(t *testing.T) {
	cx := &fakeContext{result: "## Memoria\n- algo\n"}
	rec := do(t, Services{Context: cx}, http.MethodGet, "/context?project=Kandel&scope=global", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, se esperaba 200; body=%s", rec.Code, rec.Body.String())
	}
	if cx.gotProject != "Kandel" || cx.gotScope != domain.ScopeGlobal {
		t.Errorf("argumentos mal traducidos: project=%q scope=%v", cx.gotProject, cx.gotScope)
	}
	if !strings.Contains(rec.Body.String(), "Memoria") {
		t.Errorf("el contexto no se serializó: %s", rec.Body.String())
	}
}

func TestContextBadScope(t *testing.T) {
	rec := do(t, Services{Context: &fakeContext{}}, http.MethodGet, "/context?scope=raro", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("scope inválido debe dar 400, dio %d", rec.Code)
	}
}

func TestPassiveCapture(t *testing.T) {
	mem := &fakeMemory{saveResult: service.SaveResult{Memory: domain.Memory{ID: 1}, Outcome: service.SaveCreated}}
	body := `{"session_id":"s1","project":"kandel","content":"Aprendizajes:\n- el primer aprendizaje útil\n- el segundo aprendizaje útil"}`
	rec := do(t, Services{Memory: mem}, http.MethodPost, "/memories/passive", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, se esperaba 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"saved":2`) {
		t.Errorf("se esperaban 2 guardados: %s", rec.Body.String())
	}
	if mem.lastSave.Type != domain.TypeDiscovery {
		t.Errorf("la captura pasiva debe guardar tipo discovery, fue %v", mem.lastSave.Type)
	}
}

func TestPassiveCaptureNoSection(t *testing.T) {
	mem := &fakeMemory{}
	rec := do(t, Services{Memory: mem}, http.MethodPost, "/memories/passive", `{"content":"texto sin aprendizajes"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("sin sección debe responder 200 con 0 extraídos, dio %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"extracted":0`) {
		t.Errorf("se esperaban 0 extraídos: %s", rec.Body.String())
	}
}

func TestMigrateProject(t *testing.T) {
	pr := &fakeProject{result: service.MigrateResult{Migrated: true, MemoriesUpdated: 2}}
	rec := do(t, Services{Project: pr}, http.MethodPost, "/projects/migrate", `{"old_project":"viejo","new_project":"nuevo"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, se esperaba 200; body=%s", rec.Code, rec.Body.String())
	}
	if pr.gotOld != "viejo" || pr.gotNew != "nuevo" {
		t.Errorf("argumentos mal traducidos: old=%q new=%q", pr.gotOld, pr.gotNew)
	}
	if !strings.Contains(rec.Body.String(), `"status":"migrated"`) {
		t.Errorf("respuesta inesperada: %s", rec.Body.String())
	}
}

func TestMigrateProjectSkipped(t *testing.T) {
	pr := &fakeProject{result: service.MigrateResult{Migrated: false}}
	rec := do(t, Services{Project: pr}, http.MethodPost, "/projects/migrate", `{"old_project":"a","new_project":"a"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"skipped"`) {
		t.Fatalf("sin migración debe responder skipped: %d %s", rec.Code, rec.Body.String())
	}
}

func TestMigrateProjectMissingFields(t *testing.T) {
	rec := do(t, Services{Project: &fakeProject{}}, http.MethodPost, "/projects/migrate", `{"old_project":"a"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("faltando new_project debe dar 400, dio %d", rec.Code)
	}
}

func TestUnknownRoute(t *testing.T) {
	rec := do(t, Services{}, http.MethodGet, "/no-existe", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("ruta desconocida debe dar 404, dio %d", rec.Code)
	}
}

func TestBackfillEndpointAccepts(t *testing.T) {
	emb := newFakeEmbedding()
	rec := do(t, Services{Embedding: emb}, http.MethodPost, "/embeddings/backfill", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("backfill debe responder 202, dio %d", rec.Code)
	}
	emb.waitSweep(t)
}

func TestBackfillEndpointUnavailableWithoutEmbedder(t *testing.T) {
	rec := do(t, Services{}, http.MethodPost, "/embeddings/backfill", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("sin embebedor debe responder 503, dio %d", rec.Code)
	}
}

func TestWarmupEndpointAccepts(t *testing.T) {
	emb := newFakeEmbedding()
	rec := do(t, Services{Embedding: emb}, http.MethodPost, "/embeddings/warmup", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("warmup debe responder 202, dio %d", rec.Code)
	}
	emb.waitWarmup(t)
}

func TestSyncPushEndpointAccepts(t *testing.T) {
	s := newFakeSync()
	rec := do(t, Services{Sync: s}, http.MethodPost, "/sync/push", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("sync push debe responder 202, dio %d", rec.Code)
	}
	s.waitPush(t)
}

func TestSyncPullEndpointAccepts(t *testing.T) {
	s := newFakeSync()
	rec := do(t, Services{Sync: s}, http.MethodPost, "/sync/pull", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("sync pull debe responder 202, dio %d", rec.Code)
	}
	s.waitPull(t)
}

func TestSyncEndpointsUnavailableWithoutSync(t *testing.T) {
	for _, path := range []string{"/sync/push", "/sync/pull"} {
		rec := do(t, Services{}, http.MethodPost, path, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s sin sync debe responder 503, dio %d", path, rec.Code)
		}
	}
}

// ─── dobles ────────────────────────────────────────────────────────────────────

type fakeMemory struct {
	lastSave     service.SaveInput
	lastSearch   ports.SearchQuery
	saveResult   service.SaveResult
	getResult    *domain.Memory
	updateInput  service.UpdateInput
	updateResult domain.Memory
	deletedID    int64
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
func (f *fakeMemory) Update(_ context.Context, _ int64, in service.UpdateInput) (domain.Memory, error) {
	f.updateInput = in
	return f.updateResult, nil
}
func (f *fakeMemory) Delete(_ context.Context, id int64) error {
	f.deletedID = id
	return nil
}

type fakeSession struct {
	started service.StartInput
	endErr  error
}

func (f *fakeSession) Start(_ context.Context, in service.StartInput) (domain.Session, error) {
	f.started = in
	return domain.Session{ID: in.ID, Project: in.Project}, nil
}
func (f *fakeSession) End(_ context.Context, _ string) error { return f.endErr }

type fakeLifecycle struct{}

func (f *fakeLifecycle) PendingResolution(_ context.Context, _ string, _ int) ([]domain.Memory, error) {
	return nil, nil
}

type fakePrompt struct {
	last service.SavePromptInput
}

func (f *fakePrompt) Save(_ context.Context, in service.SavePromptInput) (service.SavePromptResult, error) {
	f.last = in
	return service.SavePromptResult{Prompt: domain.Prompt{ID: 1}, Inserted: true}, nil
}

type fakeContext struct {
	result     string
	gotProject string
	gotScope   domain.Scope
}

func (f *fakeContext) Format(_ context.Context, project string, scope domain.Scope) (string, error) {
	f.gotProject = project
	f.gotScope = scope
	return f.result, nil
}

type fakeProject struct {
	result service.MigrateResult
	gotOld string
	gotNew string
}

func (f *fakeProject) Migrate(_ context.Context, oldProject, newProject string) (service.MigrateResult, error) {
	f.gotOld = oldProject
	f.gotNew = newProject
	return f.result, nil
}

// fakeEmbedding cumple EmbeddingPort. Como los endpoints corren en una goroutine, expone canales
// (creados de antemano) con los que el test espera a que el trabajo asíncrono termine (sin carreras
// con -race).
type fakeEmbedding struct {
	sweepDone  chan struct{}
	warmupDone chan struct{}
}

func newFakeEmbedding() *fakeEmbedding {
	return &fakeEmbedding{sweepDone: make(chan struct{}, 1), warmupDone: make(chan struct{}, 1)}
}

func (f *fakeEmbedding) SweepBackfill(context.Context) (service.BackfillResult, bool, error) {
	f.sweepDone <- struct{}{}
	return service.BackfillResult{}, false, nil
}
func (f *fakeEmbedding) Warmup(context.Context) error {
	f.warmupDone <- struct{}{}
	return nil
}
func (f *fakeEmbedding) waitSweep(t *testing.T) {
	t.Helper()
	select {
	case <-f.sweepDone:
	case <-time.After(time.Second):
		t.Fatal("SweepBackfill no se invocó")
	}
}
func (f *fakeEmbedding) waitWarmup(t *testing.T) {
	t.Helper()
	select {
	case <-f.warmupDone:
	case <-time.After(time.Second):
		t.Fatal("Warmup no se invocó")
	}
}

// fakeSync cumple SyncPort. Como los endpoints corren en una goroutine, expone canales (creados de
// antemano) con los que el test espera a que el trabajo asíncrono termine (sin carreras con -race).
type fakeSync struct {
	pushDone chan struct{}
	pullDone chan struct{}
}

func newFakeSync() *fakeSync {
	return &fakeSync{pushDone: make(chan struct{}, 1), pullDone: make(chan struct{}, 1)}
}

func (f *fakeSync) AutoPush(context.Context) (service.PushResult, bool, error) {
	f.pushDone <- struct{}{}
	return service.PushResult{}, false, nil
}
func (f *fakeSync) AutoPull(context.Context) (service.PullResult, bool, error) {
	f.pullDone <- struct{}{}
	return service.PullResult{}, false, nil
}
func (f *fakeSync) waitPush(t *testing.T) {
	t.Helper()
	select {
	case <-f.pushDone:
	case <-time.After(time.Second):
		t.Fatal("AutoPush no se invocó")
	}
}
func (f *fakeSync) waitPull(t *testing.T) {
	t.Helper()
	select {
	case <-f.pullDone:
	case <-time.After(time.Second):
		t.Fatal("AutoPull no se invocó")
	}
}

// TestShutdownEndpoint comprueba que, con OnShutdown cableado, POST /shutdown responde 202 y dispara
// el cierre (la goroutine puede correr tras la respuesta, así que se espera por un canal).
func TestShutdownEndpoint(t *testing.T) {
	fired := make(chan struct{}, 1)
	svc := Services{OnShutdown: func() { fired <- struct{}{} }}
	rec := do(t, svc, http.MethodPost, "/shutdown", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, se esperaba 202", rec.Code)
	}
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("OnShutdown no se disparó")
	}
}

// TestShutdownDisabledWhenNil comprueba que, sin OnShutdown, la ruta /shutdown ni siquiera existe
// (el mux responde 405 al método porque no hay handler registrado para ese patrón).
func TestShutdownDisabledWhenNil(t *testing.T) {
	rec := do(t, Services{}, http.MethodPost, "/shutdown", "")
	if rec.Code == http.StatusAccepted {
		t.Fatalf("la ruta /shutdown no debería existir sin OnShutdown; status = %d", rec.Code)
	}
}

var (
	_ MemoryPort    = (*fakeMemory)(nil)
	_ SessionPort   = (*fakeSession)(nil)
	_ LifecyclePort = (*fakeLifecycle)(nil)
	_ PromptPort    = (*fakePrompt)(nil)
	_ ContextPort   = (*fakeContext)(nil)
	_ ProjectPort   = (*fakeProject)(nil)
	_ EmbeddingPort = (*fakeEmbedding)(nil)
)
