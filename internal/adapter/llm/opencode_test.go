package llm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

const openCodeTestModel = "opencode/deepseek-v4.1-flash-free#high"

// TestOpenCodeJudgeParsesJSONL verifica el camino completo del juez: comando, prompt por stdin,
// reconstrucción del texto desde el flujo JSONL y parseo del veredicto.
func TestOpenCodeJudgeParsesJSONL(t *testing.T) {
	r := newOpenCode(openCodeTestModel)
	var gotName, gotStdin string
	var gotArgs []string
	r.runCLI = func(_ context.Context, name string, args []string, stdin string) ([]byte, error) {
		gotName, gotArgs, gotStdin = name, args, stdin
		// Dos partes de texto: el runner debe concatenarlas antes de parsear el JSON.
		return []byte(strings.Join([]string{
			`{"type":"step_start","part":{"type":"step-start"}}`,
			`{"type":"text","part":{"type":"text","text":"{\"relation\":\"supersedes\","}}`,
			`{"type":"text","part":{"type":"text","text":"\"confidence\":0.95,\"reasoning\":\"A reemplaza B\"}"}}`,
			`{"type":"step_finish","part":{"type":"step-finish"}}`,
		}, "\n")), nil
	}

	v, err := r.Judge(context.Background(), "prompt de prueba")
	if err != nil {
		t.Fatalf("Judge devolvió error: %v", err)
	}
	if gotName != "opencode" {
		t.Errorf("comando = %q, se esperaba opencode", gotName)
	}
	for _, wantArg := range []string{"run", "--archived", "--format", "json", "--model", openCodeTestModel} {
		if !slices.Contains(gotArgs, wantArg) {
			t.Errorf("args = %v, falta %q", gotArgs, wantArg)
		}
	}
	if gotStdin != "prompt de prueba" {
		t.Errorf("stdin = %q, se esperaba el prompt", gotStdin)
	}
	if v.Relation != domain.RelationSupersedes {
		t.Errorf("relación = %q, se esperaba supersedes", v.Relation)
	}
	if v.Confidence != 0.95 {
		t.Errorf("confianza = %v, se esperaba 0.95", v.Confidence)
	}
	if v.Model != openCodeTestModel {
		t.Errorf("modelo = %q, se esperaba %q (rellenado desde la config)", v.Model, openCodeTestModel)
	}
}

// TestOpenCodePlainVerdict verifica la tolerancia a una salida sin sobre de eventos (JSON suelto).
func TestOpenCodePlainVerdict(t *testing.T) {
	r := newOpenCode(openCodeTestModel)
	r.runCLI = func(context.Context, string, []string, string) ([]byte, error) {
		return []byte(`{"relation":"related","confidence":0.7,"reasoning":"mismo tema"}`), nil
	}

	v, err := r.Judge(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Judge devolvió error: %v", err)
	}
	if v.Relation != domain.RelationRelated || v.Confidence != 0.7 {
		t.Errorf("veredicto = %+v, se esperaba related/0.7", v)
	}
}

// TestOpenCodeErrorEvent verifica que un error del proveedor dentro del flujo JSONL se propaga como
// error de Go (y por tanto queda registrado en alerts.json por el liveRunner).
func TestOpenCodeErrorEvent(t *testing.T) {
	r := newOpenCode(openCodeTestModel)
	r.runCLI = func(context.Context, string, []string, string) ([]byte, error) {
		return []byte(`{"type":"error","error":{"type":"provider.quota","message":"sin cuota"}}`), nil
	}

	_, err := r.Judge(context.Background(), "prompt")
	if err == nil || !strings.Contains(err.Error(), "sin cuota") {
		t.Fatalf("se esperaba error con el motivo del proveedor, se obtuvo: %v", err)
	}
}

// TestOpenCodeEmptyOutput verifica que una salida sin texto útil se reporta como JSON inválido.
func TestOpenCodeEmptyOutput(t *testing.T) {
	r := newOpenCode(openCodeTestModel)
	r.runCLI = func(context.Context, string, []string, string) ([]byte, error) {
		return []byte(`{"type":"step_finish","part":{"type":"step-finish"}}`), nil
	}

	_, err := r.Judge(context.Background(), "prompt")
	if !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("se esperaba ErrInvalidJSON, se obtuvo: %v", err)
	}
}
