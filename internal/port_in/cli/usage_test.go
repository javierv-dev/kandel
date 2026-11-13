package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/service"
)

// fakeUsageCases registra lo que la CLI le pide, para afirmar sobre ello sin base de datos.
type fakeUsageCases struct {
	stats        []service.UsageStat
	statsTotal   bool // valor de `total` con el que se pidieron las estadísticas
	rebased      []int64
	rebaseDorm   bool
	dormantCount int
}

func (f *fakeUsageCases) RecordInjected(context.Context, []int64) error { return nil }
func (f *fakeUsageCases) RecordMentions(context.Context, []int64, string) (service.MentionResult, error) {
	return service.MentionResult{}, nil
}
func (f *fakeUsageCases) Stats(_ context.Context, _ int, total bool) ([]service.UsageStat, error) {
	f.statsTotal = total
	return f.stats, nil
}
func (f *fakeUsageCases) CountDormant(context.Context) (int, error) { return f.dormantCount, nil }
func (f *fakeUsageCases) Rebase(_ context.Context, ids []int64) error {
	f.rebased = append(f.rebased, ids...)
	return nil
}
func (f *fakeUsageCases) RebaseDormant(context.Context) (int, error) {
	f.rebaseDorm = true
	return 3, nil
}

func runUsage(t *testing.T, f *fakeUsageCases, args ...string) (string, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	err := usageCmd{}.Run(context.Background(), args, &App{Usage: f, Out: &out, Err: &errBuf})
	return out.String(), err
}

func TestUsageRebaseConIds(t *testing.T) {
	f := &fakeUsageCases{}
	out, err := runUsage(t, f, "--rebase", "213", "#424")
	if err != nil {
		t.Fatalf("usage --rebase: %v", err)
	}
	if len(f.rebased) != 2 || f.rebased[0] != 213 || f.rebased[1] != 424 {
		t.Errorf("rebased = %v, se esperaba [213 424]", f.rebased)
	}
	if !strings.Contains(out, "2 memoria(s)") || !strings.Contains(out, "totales no se tocaron") {
		t.Errorf("salida inesperada: %q", out)
	}
}

func TestUsageRebaseDormant(t *testing.T) {
	f := &fakeUsageCases{}
	out, err := runUsage(t, f, "--rebase", "--dormant")
	if err != nil {
		t.Fatalf("usage --rebase --dormant: %v", err)
	}
	if !f.rebaseDorm || !strings.Contains(out, "3 memoria(s)") {
		t.Errorf("rebaseDorm=%v salida=%q", f.rebaseDorm, out)
	}
}

// Un --rebase sin objetivo es casi seguro un tipeo: no debe reiniciar nada por defecto. Tampoco se
// mezclan ids con --dormant, ni se aceptan ids que no lo son.
func TestUsageRebaseRechazaUsosAmbiguos(t *testing.T) {
	for _, args := range [][]string{
		{"--rebase"},
		{"--rebase", "--dormant", "213"},
		{"--rebase", "abc"},
		{"--rebase", "-5"},
		{"213"}, // ids sin --rebase
	} {
		f := &fakeUsageCases{}
		if _, err := runUsage(t, f, args...); err == nil {
			t.Errorf("args %v debían fallar", args)
		}
		if len(f.rebased) != 0 || f.rebaseDorm {
			t.Errorf("args %v no debían tocar la línea base", args)
		}
	}
}

// Por defecto la vista es "desde la línea base"; --total pasa al acumulado y lo pide al servicio.
func TestUsageVistaDesdeYTotal(t *testing.T) {
	corte := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	f := &fakeUsageCases{stats: []service.UsageStat{{
		Memory: domain.Memory{ID: 424, Title: "Análisis de imágenes"},
		Usage: domain.MemoryUsage{
			MemoryID: 424, InjectedCount: 14, MentionedCount: 2,
			BaseInjected: 9, BaseMentioned: 1, BaselinedAt: &corte,
		},
	}}}

	out, err := runUsage(t, f)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if f.statsTotal {
		t.Error("por defecto no debe pedirse el total")
	}
	linea := lineaDe(out, "424")
	if !strings.Contains(linea, " 5 ") || !strings.Contains(linea, " 1 ") {
		t.Errorf("la vista por defecto debe mostrar el delta (5 y 1): %q", linea)
	}

	out, err = runUsage(t, f, "--total")
	if err != nil {
		t.Fatalf("usage --total: %v", err)
	}
	if !f.statsTotal {
		t.Error("--total debe pedir el acumulado")
	}
	linea = lineaDe(out, "424")
	if !strings.Contains(linea, " 14 ") || !strings.Contains(linea, " 2 ") {
		t.Errorf("--total debe mostrar el acumulado (14 y 2): %q", linea)
	}
}

// Sin línea base la columna DESDE dice "origen"; con ella, la fecha.
func TestUsageColumnaDesde(t *testing.T) {
	corte := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	f := &fakeUsageCases{stats: []service.UsageStat{
		{Memory: domain.Memory{ID: 1, Title: "sin corte"}, Usage: domain.MemoryUsage{MemoryID: 1, InjectedCount: 1}},
		{Memory: domain.Memory{ID: 2, Title: "con corte"}, Usage: domain.MemoryUsage{MemoryID: 2, InjectedCount: 1, BaselinedAt: &corte}},
	}}
	out, _ := runUsage(t, f)
	if !strings.Contains(lineaDe(out, "1"), "origen") {
		t.Errorf("sin línea base debe decir origen: %q", lineaDe(out, "1"))
	}
	if !strings.Contains(lineaDe(out, "2"), "2026-11-1") {
		t.Errorf("con línea base debe mostrar la fecha: %q", lineaDe(out, "2"))
	}
}

// lineaDe devuelve la primera línea de la salida cuyo primer campo es id.
func lineaDe(out, id string) string {
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == id {
			return l
		}
	}
	return ""
}
