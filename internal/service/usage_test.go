package service

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
)

// fakeUsageRepo registra en memoria lo que se le anota, para poder afirmar sobre ello.
type fakeUsageRepo struct {
	injected  []int64
	mentioned []int64
	dormant   []int64   // lo que devuelve DormantIDs
	rebased   []int64   // ids a los que se les fijó la línea base
	rebasedAt time.Time // instante con el que se fijó
}

func (f *fakeUsageRepo) RecordInjected(_ context.Context, ids []int64, _ time.Time) error {
	f.injected = append(f.injected, ids...)
	return nil
}

func (f *fakeUsageRepo) RecordMentioned(_ context.Context, ids []int64, _ time.Time) error {
	f.mentioned = append(f.mentioned, ids...)
	return nil
}

func (f *fakeUsageRepo) Get(_ context.Context, id int64) (domain.MemoryUsage, error) {
	return domain.MemoryUsage{MemoryID: id}, nil
}

func (f *fakeUsageRepo) TopInjected(_ context.Context, _ int, _ bool) ([]domain.MemoryUsage, error) {
	return nil, nil
}

func (f *fakeUsageRepo) CountDormant(_ context.Context, _ int) (int, error) { return 0, nil }

func (f *fakeUsageRepo) DormantIDs(_ context.Context, _ int) ([]int64, error) {
	return f.dormant, nil
}

func (f *fakeUsageRepo) Rebase(_ context.Context, ids []int64, at time.Time) error {
	f.rebased = append(f.rebased, ids...)
	f.rebasedAt = at
	return nil
}

// Rebase de MemoryService y de UsageService comparten fake: el contrato es el mismo.
var _ UsageRebaser = (*fakeUsageRepo)(nil)

// RebaseDormant fija la línea base de TODAS las dormidas y de nada más, y cuenta cuántas fueron.
func TestRebaseDormantFijaSoloLasDormidas(t *testing.T) {
	svc, usage, _ := newUsageFixture(t, "irrelevante")
	usage.dormant = []int64{213, 424}

	n, err := svc.RebaseDormant(context.Background())
	if err != nil {
		t.Fatalf("RebaseDormant: %v", err)
	}
	if n != 2 || len(usage.rebased) != 2 || usage.rebased[0] != 213 || usage.rebased[1] != 424 {
		t.Errorf("n=%d rebased=%v, se esperaba 2 y [213 424]", n, usage.rebased)
	}
}

// Sin dormidas no hay nada que fijar: no es un error y no toca el repo.
func TestRebaseDormantSinDormidas(t *testing.T) {
	svc, usage, _ := newUsageFixture(t, "irrelevante")
	n, err := svc.RebaseDormant(context.Background())
	if err != nil || n != 0 || len(usage.rebased) != 0 {
		t.Errorf("n=%d err=%v rebased=%v, se esperaba 0, nil y nada", n, err, usage.rebased)
	}
}

// writeTranscript escribe una transcripción JSONL temporal y devuelve su ruta.
func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("escribir transcripción: %v", err)
	}
	return path
}

// newUsageFixture arma el servicio con una memoria de título conocido ya guardada.
func newUsageFixture(t *testing.T, title string) (*UsageService, *fakeUsageRepo, int64) {
	t.Helper()
	memories := newFakeObs()
	id, err := memories.Add(context.Background(), domain.Memory{
		Project: "kandel", Type: domain.TypeDecision, Title: title, Content: "cuerpo",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	usage := &fakeUsageRepo{}
	return NewUsageService(usage, memories, fixedClock{now: time.Now()}), usage, id
}

// Este es el punto que decide si la medición sirve para algo. El bloque inyectado viaja DENTRO del
// turno del usuario, así que el título de toda memoria inyectada aparece siempre en la
// transcripción. Si se contara sin filtrar por rol, todo daría "mencionado" y el dato no mediría
// nada.
func TestRecordMentionsIgnoraElBloqueInyectado(t *testing.T) {
	const title = "Umbral de relevancia se queda en 0.45"
	svc, usage, id := newUsageFixture(t, title)

	// El usuario trae la memoria inyectada (título incluido); el agente responde sin usarla.
	path := writeTranscript(t,
		`{"type":"user","message":{"role":"user","content":"`+title+`\n\n¿qué hora es?"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"text":"Son las tres."}]}}`,
	)

	res, err := svc.RecordMentions(context.Background(), []int64{id}, path)
	if err != nil {
		t.Fatalf("RecordMentions: %v", err)
	}
	if res.Mentioned != 0 {
		t.Errorf("menciones = %d, se esperaba 0: el título solo estaba en el turno del usuario", res.Mentioned)
	}
	if len(usage.mentioned) != 0 {
		t.Errorf("no debía anotarse ninguna mención, se anotó %v", usage.mentioned)
	}
	if res.Scanned != 1 {
		t.Errorf("scanned = %d, se esperaba 1", res.Scanned)
	}
}

// El caso positivo: el agente cita el id como lo superficia el contexto inyectado ("id 7").
func TestRecordMentionsDetectaCitaPorID(t *testing.T) {
	svc, usage, id := newUsageFixture(t, "Una decisión con título largo de sobra")

	path := writeTranscript(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"text":"Según la memoria id `+itoa(id)+` esto ya se decidió."}]}}`,
	)

	res, err := svc.RecordMentions(context.Background(), []int64{id}, path)
	if err != nil {
		t.Fatalf("RecordMentions: %v", err)
	}
	if res.Mentioned != 1 || len(usage.mentioned) != 1 || usage.mentioned[0] != id {
		t.Errorf("se esperaba 1 mención de la memoria %d, fue %d (anotadas: %v)", id, res.Mentioned, usage.mentioned)
	}
}

// El caso positivo por título: el agente parafrasea pero nombra la memoria.
func TestRecordMentionsDetectaCitaPorTitulo(t *testing.T) {
	const title = "No inyectar memorias de la sesión en curso"
	svc, _, id := newUsageFixture(t, title)

	path := writeTranscript(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"text":"Aplico lo de No inyectar memorias de la sesión en curso."}]}}`,
	)

	res, _ := svc.RecordMentions(context.Background(), []int64{id}, path)
	if res.Mentioned != 1 {
		t.Errorf("un título citado literalmente debía contar como mención, fue %d", res.Mentioned)
	}
}

// Un título corto y genérico aparecería por casualidad en cualquier conversación del proyecto. Se
// exige un mínimo de longitud para que la señal no se convierta en ruido.
func TestRecordMentionsIgnoraTitulosCortos(t *testing.T) {
	svc, _, id := newUsageFixture(t, "Usar Go")

	path := writeTranscript(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"text":"Vamos a usar go build para compilar."}]}}`,
	)

	res, _ := svc.RecordMentions(context.Background(), []int64{id}, path)
	if res.Mentioned != 0 {
		t.Errorf("un título corto no debe contar como mención, fue %d", res.Mentioned)
	}
}

// Cerrar la sesión no puede fallar porque la transcripción no esté donde se dijo: se mide de menos,
// no se rompe el cierre.
func TestRecordMentionsSinTranscripcionNoFalla(t *testing.T) {
	svc, _, id := newUsageFixture(t, "Una decisión con título largo de sobra")

	if _, err := svc.RecordMentions(context.Background(), []int64{id}, "/no/existe/transcript.jsonl"); err != nil {
		t.Errorf("una transcripción ausente no debe fallar: %v", err)
	}
}

// Una línea corrupta a mitad no debe abortar el barrido: el formato lo fija el agente y una versión
// nueva no puede dejar la medición a cero.
func TestRecordMentionsToleraLineasCorruptas(t *testing.T) {
	svc, _, id := newUsageFixture(t, "Una decisión con título largo de sobra")

	path := writeTranscript(t,
		`{"type":"assistant","message":{"role":"assistant",`,
		`no es json en absoluto`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"text":"revisa la id `+itoa(id)+`"}]}}`,
	)

	res, err := svc.RecordMentions(context.Background(), []int64{id}, path)
	if err != nil {
		t.Fatalf("RecordMentions: %v", err)
	}
	if res.Mentioned != 1 {
		t.Errorf("la línea válida tras las corruptas debía contarse, menciones = %d", res.Mentioned)
	}
}

// RecordInjected es el camino caliente (una vez por prompt): anota tal cual y sin sorpresas.
func TestRecordInjected(t *testing.T) {
	svc, usage, _ := newUsageFixture(t, "Una decisión con título largo de sobra")

	if err := svc.RecordInjected(context.Background(), []int64{4, 8}); err != nil {
		t.Fatalf("RecordInjected: %v", err)
	}
	if len(usage.injected) != 2 {
		t.Errorf("se esperaban 2 ids anotados, fueron %v", usage.injected)
	}
	// Sin ids no debe tocar el repositorio (el caso normal: prompt sin memorias afines).
	usage.injected = nil
	if err := svc.RecordInjected(context.Background(), nil); err != nil {
		t.Fatalf("RecordInjected sin ids: %v", err)
	}
	if len(usage.injected) != 0 {
		t.Errorf("sin ids no debía anotarse nada, se anotó %v", usage.injected)
	}
}

// itoa acorta la interpolación del id dentro de las transcripciones de prueba.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }
