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
		`{"title":"T","content":"C","type":"decision","scope":"personal","project":"kandel","topic_key":"auth/login"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, se esperaba 201; body=%s", rec.Code, rec.Body.String())
	}
	if mem.lastSave.Title != "T" || mem.lastSave.Type != domain.TypeDecision || mem.lastSave.Scope != domain.ScopePersonal {
		t.Errorf("cuerpo mal traducido: %+v", mem.lastSave)
	}
	if mem.lastSave.TopicKey != "auth/login" || mem.lastSave.Project != "kandel" {
		t.Errorf("topic/project mal traducidos: %+v", mem.lastSave)
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

func TestDeleteMemoryHard(t *testing.T) {
	mem := &fakeMemory{}
	rec := do(t, Services{Memory: mem}, http.MethodDelete, "/memories/4?hard=true", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, se esperaba 204", rec.Code)
	}
	if mem.deletedID != 4 || !mem.deletedHard {
		t.Errorf("delete mal traducido: id=%d hard=%v", mem.deletedID, mem.deletedHard)
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

func TestPinAndUnpin(t *testing.T) {
	life := &fakeLifecycle{}
	svc := Services{Lifecycle: life}
	if rec := do(t, svc, http.MethodPost, "/memories/5/pin", ""); rec.Code != http.StatusOK {
		t.Fatalf("pin status = %d", rec.Code)
	}
	if life.pinnedID != 5 {
		t.Errorf("pinnedID = %d, se esperaba 5", life.pinnedID)
	}
	if rec := do(t, svc, http.MethodPost, "/memories/5/unpin", ""); rec.Code != http.StatusOK {
		t.Fatalf("unpin status = %d", rec.Code)
	}
	if life.unpinnedID != 5 {
		t.Errorf("unpinnedID = %d, se esperaba 5", life.unpinnedID)
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
	rec := do(t, Services{Session: sess}, http.MethodPost, "/sessions/x/end", `{"summary":"fin"}`)
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
	rec := do(t, Services{Context: cx}, http.MethodGet, "/context?project=Kandel&scope=personal", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, se esperaba 200; body=%s", rec.Code, rec.Body.String())
	}
	if cx.gotProject != "Kandel" || cx.gotScope != domain.ScopePersonal {
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
	if mem.lastSave.Type != domain.TypeLearning {
		t.Errorf("la captura pasiva debe guardar tipo learning, fue %v", mem.lastSave.Type)
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

// ─── dobles ────────────────────────────────────────────────────────────────────

type fakeMemory struct {
	lastSave     service.SaveInput
	lastSearch   ports.SearchQuery
	saveResult   service.SaveResult
	getResult    *domain.Memory
	updateInput  service.UpdateInput
	updateResult domain.Memory
	deletedID    int64
	deletedHard  bool
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
func (f *fakeMemory) Delete(_ context.Context, id int64, hard bool) error {
	f.deletedID = id
	f.deletedHard = hard
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
func (f *fakeSession) End(_ context.Context, _, _ string) error { return f.endErr }
func (f *fakeSession) Get(_ context.Context, id string) (*domain.Session, error) {
	if id == "missing" {
		return nil, nil
	}
	return &domain.Session{ID: id}, nil
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

var (
	_ MemoryPort    = (*fakeMemory)(nil)
	_ SessionPort   = (*fakeSession)(nil)
	_ LifecyclePort = (*fakeLifecycle)(nil)
	_ PromptPort    = (*fakePrompt)(nil)
	_ ContextPort   = (*fakeContext)(nil)
	_ ProjectPort   = (*fakeProject)(nil)
	_ EmbeddingPort = (*fakeEmbedding)(nil)
)
