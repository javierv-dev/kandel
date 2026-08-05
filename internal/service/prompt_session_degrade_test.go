package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
)

// --- Seguimiento de la auditoría de durabilidad (docs/auditoria-durabilidad.md §5.bis) ---
//
// Al pasar a aplicarse las claves foráneas en TODAS las conexiones, un prompt cuya sesión no
// existiera dejó de entrar huérfano y pasó a rechazarse. Estos tests fijan la política elegida: el
// contenido del prompt es el dato durable y se conserva; la sesión es solo el contenedor que lo
// liga a su conversación, y es lo que se sacrifica.

// promptsConFK envuelve el doble normal de prompts añadiendo la restricción que aplica SQLite:
// rechaza con ErrUnknownSession cualquier prompt cuya sesión no esté registrada.
type promptsConFK struct {
	*fakePrompts
	sesiones map[string]bool
}

func newPromptsConFK(sesiones ...string) *promptsConFK {
	set := make(map[string]bool, len(sesiones))
	for _, s := range sesiones {
		set[s] = true
	}
	return &promptsConFK{fakePrompts: newFakePrompts(), sesiones: set}
}

func (f *promptsConFK) AddIfMissing(ctx context.Context, p domain.Prompt) (int64, bool, error) {
	if p.SessionID != "" && !f.sesiones[p.SessionID] {
		return 0, false, domain.ErrUnknownSession
	}
	return f.fakePrompts.AddIfMissing(ctx, p)
}

func newDegradeService(prompts *promptsConFK) *PromptService {
	return NewPromptService(prompts, &fakeSync{}, passthroughUoW{},
		fixedClock{now: time.Unix(1700000000, 0).UTC()})
}

// TestPromptSaveDegradaSesionInexistente: un prompt cuya sesión no existe se guarda igualmente, sin
// sesión, en vez de perderse.
func TestPromptSaveDegradaSesionInexistente(t *testing.T) {
	prompts := newPromptsConFK() // ninguna sesión existe
	svc := newDegradeService(prompts)

	res, err := svc.Save(context.Background(), SavePromptInput{
		SessionID: "sesion-que-no-existe",
		Project:   "kandel",
		Content:   "lo que el usuario pidió",
	})
	if err != nil {
		t.Fatalf("Save devolvió error; el prompt debía conservarse: %v", err)
	}
	if !res.SessionDropped {
		t.Error("SessionDropped = false; debería avisar de que se guardó sin sesión")
	}
	if res.Prompt.SessionID != "" {
		t.Errorf("SessionID = %q, se esperaba vacío", res.Prompt.SessionID)
	}
	if len(prompts.items) != 1 {
		t.Fatalf("se guardaron %d prompts, se esperaba 1", len(prompts.items))
	}
	if prompts.items[0].Content != "lo que el usuario pidió" {
		t.Errorf("el contenido no se conservó: %q", prompts.items[0].Content)
	}
}

// TestPromptSaveConservaSesionExistente: la degradación NO se dispara en el camino normal.
func TestPromptSaveConservaSesionExistente(t *testing.T) {
	prompts := newPromptsConFK("sesion-viva")
	svc := newDegradeService(prompts)

	res, err := svc.Save(context.Background(), SavePromptInput{
		SessionID: "sesion-viva",
		Project:   "kandel",
		Content:   "un prompt normal",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if res.SessionDropped {
		t.Error("SessionDropped = true con una sesión que sí existe")
	}
	if res.Prompt.SessionID != "sesion-viva" {
		t.Errorf("SessionID = %q, se esperaba \"sesion-viva\"", res.Prompt.SessionID)
	}
}

// TestPromptSavePropagaOtrosErrores: la degradación es específica de la sesión inexistente y no se
// traga cualquier fallo. Un error de escritura tiene que seguir llegando arriba; tragárselo sería
// reintroducir la pérdida silenciosa que la auditoría vino a corregir.
func TestPromptSavePropagaOtrosErrores(t *testing.T) {
	fallo := errors.New("sqlite: disco lleno")
	prompts := &promptsQueFallan{fakePrompts: newFakePrompts(), err: fallo}
	svc := NewPromptService(prompts, &fakeSync{}, passthroughUoW{},
		fixedClock{now: time.Unix(1700000000, 0).UTC()})

	if _, err := svc.Save(context.Background(), SavePromptInput{
		SessionID: "s", Project: "kandel", Content: "x",
	}); !errors.Is(err, fallo) {
		t.Fatalf("err = %v; un fallo que no sea de sesión debe propagarse", err)
	}
}

// promptsQueFallan devuelve siempre el mismo error al guardar.
type promptsQueFallan struct {
	*fakePrompts
	err error
}

func (f *promptsQueFallan) AddIfMissing(context.Context, domain.Prompt) (int64, bool, error) {
	return 0, false, f.err
}
