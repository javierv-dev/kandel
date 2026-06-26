package diagnostic

import (
	"context"
	"errors"
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

var (
	_ EmbedderProbe     = fakeEmbedder{}
	_ ports.VectorIndex = fakeVectors{}
	_ Check             = EmbedderCheck{}
	_ Check             = PendingEmbeddingsCheck{}
)
