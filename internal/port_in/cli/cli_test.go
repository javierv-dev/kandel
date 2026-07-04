package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/diagnostic"
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
	restoredID  int64
	getID       int64
	getResult   *domain.Memory
	saveErr     error
	projects    []string
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

func (f *fakeMemory) Delete(_ context.Context, id int64) error {
	f.deletedID = id
	return nil
}

func (f *fakeMemory) Restore(_ context.Context, id int64) (domain.Memory, error) {
	f.restoredID = id
	return domain.Memory{ID: id}, nil
}

func (f *fakeMemory) Projects(_ context.Context) ([]string, error) {
	return f.projects, nil
}

// fakeLifecycle registra las llamadas de ciclo de vida.
type fakeLifecycle struct{}

func (f *fakeLifecycle) PendingResolution(_ context.Context, _ string, _ int) ([]domain.Memory, error) {
	return nil, nil
}

// fakeEmbedding registra las llamadas de mantenimiento de vectores.
type fakeEmbedding struct {
	backfillCalled bool
	warmupCalled   bool
	reembedID      int64
	backfillResult service.BackfillResult
}

func (f *fakeEmbedding) Backfill(context.Context) (service.BackfillResult, error) {
	f.backfillCalled = true
	return f.backfillResult, nil
}
func (f *fakeEmbedding) SweepBackfill(context.Context) (service.BackfillResult, bool, error) {
	f.backfillCalled = true
	return f.backfillResult, false, nil
}
func (f *fakeEmbedding) Warmup(context.Context) error {
	f.warmupCalled = true
	return nil
}
func (f *fakeEmbedding) Reembed(_ context.Context, id int64) error {
	f.reembedID = id
	return nil
}

// fakeDoctor devuelve un reporte preprogramado.
type fakeDoctor struct {
	report diagnostic.Report
}

func (f fakeDoctor) Run(context.Context) diagnostic.Report { return f.report }

// fakeConflict registra las llamadas de conflictos.
type fakeConflict struct {
	candidates []service.Candidate
	findID     int64
}

func (f *fakeConflict) FindCandidates(_ context.Context, id int64, _ int) ([]service.Candidate, error) {
	f.findID = id
	return f.candidates, nil
}
func (f *fakeConflict) Compare(_ context.Context, _, _ int64) (domain.MemoryRelation, error) {
	return domain.MemoryRelation{}, nil
}
func (f *fakeConflict) RecordVerdict(_ context.Context, _, _ int64, _ domain.Verdict) (domain.MemoryRelation, error) {
	return domain.MemoryRelation{}, nil
}

// fakeSync registra las llamadas de sincronización.
type fakeSync struct {
	exportResult service.ExportResult
	importResult service.ImportResult
	exported     bool
	imported     bool
}

func (f *fakeSync) Export(context.Context) (service.ExportResult, error) {
	f.exported = true
	return f.exportResult, nil
}
func (f *fakeSync) Import(context.Context) (service.ImportResult, error) {
	f.imported = true
	return f.importResult, nil
}

// fakeSeed registra las llamadas del seed para comprobar el flujo del comando.
type fakeSeed struct {
	candidates   []service.SeedCandidate
	genInput     service.SeedGenerateInput
	genErr       error
	saved        []service.SeedCandidate
	savedProject string
	saveResult   service.SeedSaveResult
}

func (f *fakeSeed) Generate(_ context.Context, in service.SeedGenerateInput) ([]service.SeedCandidate, error) {
	f.genInput = in
	return f.candidates, f.genErr
}

func (f *fakeSeed) Save(_ context.Context, c []service.SeedCandidate, project string) (service.SeedSaveResult, error) {
	f.saved = c
	f.savedProject = project
	return f.saveResult, nil
}

// newTestApp construye un App con dobles y buffers de salida capturables.
func newTestApp() (*App, *fakeMemory, *fakeLifecycle, *bytes.Buffer, *bytes.Buffer) {
	mem := &fakeMemory{}
	life := &fakeLifecycle{}
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	app := &App{Memory: mem, Lifecycle: life, Embedding: &fakeEmbedding{}, Doctor: fakeDoctor{}, Conflict: &fakeConflict{}, Sync: &fakeSync{}, Out: out, Err: errBuf, Version: "test"}
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
	got := out.String()
	if !strings.Contains(got, "kandel test") {
		t.Fatalf("salida de version inesperada: %q", got)
	}
	// Metadatos de build (§21 Eje 5): commit/fecha sin inyectar => "desconocido"; schema presente.
	for _, want := range []string{"commit:  desconocido", "built:   desconocido", "schema:  0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("la salida de version no contiene %q: %q", want, got)
		}
	}
}

func TestDispatchHelpListsRegisteredCommands(t *testing.T) {
	app, _, _, out, _ := newTestApp()
	if err := Dispatch(context.Background(), nil, app); err != nil {
		t.Fatalf("ayuda devolvió error: %v", err)
	}
	for _, name := range []string{"session", "embeddings", "doctor", "sync", "seed", "tui"} {
		if !strings.Contains(out.String(), name) {
			t.Errorf("la ayuda no lista el comando %q:\n%s", name, out.String())
		}
	}
}

func TestEmbeddingsBackfill(t *testing.T) {
	app, _, _, _, _ := newTestApp()
	emb := &fakeEmbedding{backfillResult: service.BackfillResult{Embedded: 3}}
	app.Embedding = emb
	if err := Dispatch(context.Background(), []string{"embeddings", "backfill"}, app); err != nil {
		t.Fatalf("embeddings backfill devolvió error: %v", err)
	}
	if !emb.backfillCalled {
		t.Error("no se llamó a Backfill")
	}
}

func TestEmbeddingsReembedParsesID(t *testing.T) {
	app, _, _, _, _ := newTestApp()
	emb := &fakeEmbedding{}
	app.Embedding = emb
	if err := Dispatch(context.Background(), []string{"embeddings", "reembed", "8"}, app); err != nil {
		t.Fatalf("embeddings reembed devolvió error: %v", err)
	}
	if emb.reembedID != 8 {
		t.Errorf("reembedID = %d, se esperaba 8", emb.reembedID)
	}
}

func TestEmbeddingsUnknownSubcommand(t *testing.T) {
	app, _, _, _, _ := newTestApp()
	if err := Dispatch(context.Background(), []string{"embeddings", "frobnicate"}, app); err == nil {
		t.Fatal("se esperaba error por subcomando desconocido")
	}
}

func TestDoctorHealthy(t *testing.T) {
	app, _, _, out, _ := newTestApp()
	app.Doctor = fakeDoctor{report: diagnostic.Report{Worst: diagnostic.SeverityInfo}}
	if err := Dispatch(context.Background(), []string{"doctor"}, app); err != nil {
		t.Fatalf("doctor sano devolvió error: %v", err)
	}
	if !strings.Contains(out.String(), "Todo en orden") {
		t.Errorf("salida inesperada: %q", out.String())
	}
}

func TestDoctorErrorFails(t *testing.T) {
	app, _, _, _, _ := newTestApp()
	app.Doctor = fakeDoctor{report: diagnostic.Report{
		Worst:    diagnostic.SeverityError,
		Findings: []diagnostic.Finding{{Check: "x", Severity: diagnostic.SeverityError, Message: "roto"}},
	}}
	if err := Dispatch(context.Background(), []string{"doctor"}, app); err == nil {
		t.Fatal("doctor con error debe devolver error")
	}
}

func TestDoctorWarningDoesNotFail(t *testing.T) {
	app, _, _, _, _ := newTestApp()
	app.Doctor = fakeDoctor{report: diagnostic.Report{
		Worst:    diagnostic.SeverityWarning,
		Findings: []diagnostic.Finding{{Check: "x", Severity: diagnostic.SeverityWarning, Message: "ojo"}},
	}}
	if err := Dispatch(context.Background(), []string{"doctor"}, app); err != nil {
		t.Fatalf("un warning no debe hacer fallar doctor: %v", err)
	}
}

func TestSyncExportByDefault(t *testing.T) {
	app, _, _, out, _ := newTestApp()
	sync := &fakeSync{exportResult: service.ExportResult{ChunkID: "abc", Count: 2}}
	app.Sync = sync
	if err := Dispatch(context.Background(), []string{"sync"}, app); err != nil {
		t.Fatalf("sync devolvió error: %v", err)
	}
	if !sync.exported || sync.imported {
		t.Error("sync sin flags debe exportar, no importar")
	}
	if !strings.Contains(out.String(), "abc") {
		t.Errorf("debe informar el chunk exportado: %q", out.String())
	}
}

func TestSyncImportFlag(t *testing.T) {
	app, _, _, _, _ := newTestApp()
	sync := &fakeSync{importResult: service.ImportResult{ChunksImported: 1, MutationsApplied: 3}}
	app.Sync = sync
	if err := Dispatch(context.Background(), []string{"sync", "--import"}, app); err != nil {
		t.Fatalf("sync --import devolvió error: %v", err)
	}
	if !sync.imported || sync.exported {
		t.Error("sync --import debe importar, no exportar")
	}
}

// writeSeedRepo crea un directorio temporal con algo de documentación para que el recolector de
// fuentes del seed no devuelva vacío.
func writeSeedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Proyecto\nUsa Postgres como base de datos."), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSeedDryRunDoesNotSave(t *testing.T) {
	app, _, _, out, _ := newTestApp()
	seed := &fakeSeed{candidates: []service.SeedCandidate{
		{Type: "decision", Scope: "project", Title: "Motor de BD", Content: "Postgres"},
	}}
	app.Seed = seed
	dir := writeSeedRepo(t)

	args := []string{"seed", "--dir", dir, "--agent", "claude", "--dry-run"}
	if err := Dispatch(context.Background(), args, app); err != nil {
		t.Fatalf("seed --dry-run devolvió error: %v", err)
	}
	if seed.saved != nil {
		t.Errorf("dry-run no debe guardar nada, guardó %d", len(seed.saved))
	}
	if !strings.Contains(out.String(), "Motor de BD") {
		t.Errorf("debe listar el candidato: %q", out.String())
	}
	// El agente y las fuentes deben llegar al servicio.
	if seed.genInput.AgentCLI != "claude" || !strings.Contains(seed.genInput.Sources, "Postgres") {
		t.Errorf("entrada de Generate inesperada: %+v", seed.genInput)
	}
}

func TestSeedConfirmYesSaves(t *testing.T) {
	app, _, _, _, _ := newTestApp()
	seed := &fakeSeed{candidates: []service.SeedCandidate{
		{Type: "decision", Scope: "project", Title: "Motor de BD", Content: "Postgres"},
	}}
	app.Seed = seed
	app.In = strings.NewReader("y\n")
	dir := writeSeedRepo(t)

	args := []string{"seed", "--dir", dir, "--agent", "claude", "--project", "kandel"}
	if err := Dispatch(context.Background(), args, app); err != nil {
		t.Fatalf("seed devolvió error: %v", err)
	}
	if len(seed.saved) != 1 {
		t.Fatalf("se esperaba 1 guardado tras confirmar, hubo %d", len(seed.saved))
	}
	if seed.savedProject != "kandel" {
		t.Errorf("proyecto guardado = %q, se esperaba kandel", seed.savedProject)
	}
}

func TestSeedConfirmNoAborts(t *testing.T) {
	app, _, _, _, _ := newTestApp()
	seed := &fakeSeed{candidates: []service.SeedCandidate{
		{Type: "decision", Scope: "project", Title: "X", Content: "y"},
	}}
	app.Seed = seed
	app.In = strings.NewReader("n\n")
	dir := writeSeedRepo(t)

	if err := Dispatch(context.Background(), []string{"seed", "--dir", dir, "--agent", "claude"}, app); err != nil {
		t.Fatalf("seed devolvió error: %v", err)
	}
	if seed.saved != nil {
		t.Errorf("una respuesta negativa no debe guardar nada")
	}
}

func TestSeedNoAgentFails(t *testing.T) {
	t.Setenv("KANDEL_AGENT_CLI", "")
	t.Setenv("KANDEL_DATA_DIR", t.TempDir()) // sin config → no hay agente resoluble
	app, _, _, _, _ := newTestApp()
	app.Seed = &fakeSeed{}
	dir := writeSeedRepo(t)
	if err := Dispatch(context.Background(), []string{"seed", "--dir", dir}, app); err == nil {
		t.Fatal("seed sin agente configurado debe fallar")
	}
}
