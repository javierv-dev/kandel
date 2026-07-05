package diagnostic

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/ports"
)

// fakeEmbedder cumple EmbedderProbe; failWith fuerza un fallo del sondeo.
type fakeEmbedder struct{ failWith error }

func (f fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	return []float32{1, 0}, nil
}
func (fakeEmbedder) Model() string { return "fake-model" }

// fakeVectors cumple ports.VectorIndex; solo PendingEmbedding se ejercita.
type fakeVectors struct {
	pending []int64
	err     error
}

func (f fakeVectors) Upsert(context.Context, int64, []float32, string) error { return nil }
func (fakeVectors) Nearest(context.Context, []float32, int) ([]ports.Neighbor, error) {
	return nil, nil
}
func (fakeVectors) Delete(context.Context, int64) error { return nil }
func (f fakeVectors) PendingEmbedding(context.Context, string, int) ([]int64, error) {
	return f.pending, f.err
}

func TestRunnerAggregatesWorstSeverity(t *testing.T) {
	r := NewRunner(
		EmbedderCheck{Embedder: fakeEmbedder{}},                           // info
		PendingEmbeddingsCheck{Vectors: fakeVectors{pending: []int64{1}}}, // warning
	)
	report := r.Run(context.Background())
	if report.Worst != SeverityWarning {
		t.Errorf("worst = %q, se esperaba warning", report.Worst)
	}
	if report.OK() {
		t.Error("con un warning, OK() debe ser false")
	}
	if len(report.Findings) != 2 {
		t.Errorf("findings = %d, se esperaban 2", len(report.Findings))
	}
}

func TestRunnerAllHealthy(t *testing.T) {
	r := NewRunner(
		EmbedderCheck{Embedder: fakeEmbedder{}},
		PendingEmbeddingsCheck{Vectors: fakeVectors{pending: nil}},
	)
	report := r.Run(context.Background())
	if !report.OK() || report.Worst != SeverityInfo {
		t.Errorf("todo sano debe dar OK con worst=info: %+v", report)
	}
}

func TestEmbedderCheckDownIsWarning(t *testing.T) {
	f := EmbedderCheck{Embedder: fakeEmbedder{failWith: errors.New("conexión rechazada")}}.Run(context.Background())
	if len(f) != 1 || f[0].Severity != SeverityWarning {
		t.Fatalf("embebedor caído debe dar un warning: %+v", f)
	}
	if f[0].NextStep == "" {
		t.Error("el warning del embebedor debe sugerir un siguiente paso")
	}
}

func TestEmbedderCheckNilIsWarning(t *testing.T) {
	f := EmbedderCheck{Embedder: nil}.Run(context.Background())
	if len(f) != 1 || f[0].Severity != SeverityWarning {
		t.Fatalf("sin embebedor debe dar warning: %+v", f)
	}
}

func TestPendingEmbeddingsErrorIsError(t *testing.T) {
	f := PendingEmbeddingsCheck{Vectors: fakeVectors{err: errors.New("db caída")}}.Run(context.Background())
	if len(f) != 1 || f[0].Severity != SeverityError {
		t.Fatalf("fallo de consulta debe dar error: %+v", f)
	}
}

func TestPendingEmbeddingsNilVectorsSkips(t *testing.T) {
	if f := (PendingEmbeddingsCheck{Vectors: nil}).Run(context.Background()); f != nil {
		t.Errorf("sin índice no debe producir hallazgos: %+v", f)
	}
}

// Con el contenido al día pero títulos pendientes, el chequeo debe avisar (warning) y mencionar el título.
func TestPendingEmbeddingsTitlePendingIsWarning(t *testing.T) {
	f := PendingEmbeddingsCheck{
		Vectors:      fakeVectors{pending: nil},           // contenido al día
		TitleVectors: fakeVectors{pending: []int64{1, 2}}, // dos títulos pendientes
	}.Run(context.Background())
	if len(f) != 1 || f[0].Severity != SeverityWarning {
		t.Fatalf("títulos pendientes deben dar warning: %+v", f)
	}
	if !strings.Contains(f[0].Message, "título") {
		t.Errorf("el mensaje debe mencionar el título: %q", f[0].Message)
	}
}

// Con contenido y título al día, el chequeo es info.
func TestPendingEmbeddingsAllHealthyWithTitle(t *testing.T) {
	f := PendingEmbeddingsCheck{
		Vectors:      fakeVectors{pending: nil},
		TitleVectors: fakeVectors{pending: nil},
	}.Run(context.Background())
	if len(f) != 1 || f[0].Severity != SeverityInfo {
		t.Errorf("contenido y título al día deben dar info: %+v", f)
	}
}

// fakeAssets cumple InstalledAssetsProbe.
type fakeAssets struct {
	version string
	present bool
	err     error
}

func (f fakeAssets) InstalledVersion() (string, bool, error) {
	return f.version, f.present, f.err
}

func TestInstalledAssetsMismatchIsWarning(t *testing.T) {
	f := InstalledAssetsCheck{Running: "v2.0.0", Probe: fakeAssets{version: "v1.0.0", present: true}}.Run(context.Background())
	if len(f) != 1 || f[0].Severity != SeverityWarning {
		t.Fatalf("assets desalineados deben dar warning: %+v", f)
	}
	if f[0].NextStep == "" {
		t.Error("el warning de desalineación debe sugerir reinstalar")
	}
}

func TestInstalledAssetsMatchIsInfo(t *testing.T) {
	f := InstalledAssetsCheck{Running: "v2.0.0", Probe: fakeAssets{version: "v2.0.0", present: true}}.Run(context.Background())
	if len(f) != 1 || f[0].Severity != SeverityInfo {
		t.Fatalf("assets al día deben dar info: %+v", f)
	}
}

func TestInstalledAssetsNotPresentIsInfo(t *testing.T) {
	f := InstalledAssetsCheck{Running: "v2.0.0", Probe: fakeAssets{present: false}}.Run(context.Background())
	if len(f) != 1 || f[0].Severity != SeverityInfo {
		t.Fatalf("sin instalación previa debe dar info: %+v", f)
	}
}

func TestInstalledAssetsNilProbeSkips(t *testing.T) {
	if f := (InstalledAssetsCheck{Running: "v2.0.0"}).Run(context.Background()); f != nil {
		t.Errorf("sin probe no debe producir hallazgos: %+v", f)
	}
}

// fakeRelease cumple ports.ReleaseChecker.
type fakeRelease struct {
	latest string
	newer  bool
}

func (f fakeRelease) Newer(context.Context, string) (string, bool) { return f.latest, f.newer }

func TestLatestReleaseNewerIsWarning(t *testing.T) {
	f := LatestReleaseCheck{Running: "v1.0.0", Checker: fakeRelease{latest: "v1.1.0", newer: true}}.Run(context.Background())
	if len(f) != 1 || f[0].Severity != SeverityWarning {
		t.Fatalf("versión nueva debe dar warning: %+v", f)
	}
	if f[0].NextStep == "" {
		t.Error("el warning de versión nueva debe sugerir actualizar")
	}
}

func TestLatestReleaseUpToDateIsInfo(t *testing.T) {
	f := LatestReleaseCheck{Running: "v1.1.0", Checker: fakeRelease{latest: "v1.1.0", newer: false}}.Run(context.Background())
	if len(f) != 1 || f[0].Severity != SeverityInfo {
		t.Fatalf("al día debe dar info: %+v", f)
	}
}

func TestLatestReleaseUndeterminedSkips(t *testing.T) {
	// latest vacío = no se pudo determinar (offline/desactivado) → sin hallazgos.
	if f := (LatestReleaseCheck{Running: "v1.0.0", Checker: fakeRelease{latest: "", newer: false}}).Run(context.Background()); f != nil {
		t.Errorf("sin determinar no debe reportar: %+v", f)
	}
}

func TestLatestReleaseNilCheckerSkips(t *testing.T) {
	if f := (LatestReleaseCheck{Running: "v1.0.0"}).Run(context.Background()); f != nil {
		t.Errorf("sin checker no debe reportar: %+v", f)
	}
}

var (
	_ EmbedderProbe        = fakeEmbedder{}
	_ ports.VectorIndex    = fakeVectors{}
	_ InstalledAssetsProbe = fakeAssets{}
	_ ports.ReleaseChecker = fakeRelease{}
	_ Check                = EmbedderCheck{}
	_ Check                = PendingEmbeddingsCheck{}
	_ Check                = InstalledAssetsCheck{}
	_ Check                = LatestReleaseCheck{}
)
