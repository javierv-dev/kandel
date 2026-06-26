package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

// fakeCLI construye una función runCLI que devuelve una salida fija (o un error).
func fakeCLI(out string, err error) func(context.Context, string, []string, string) ([]byte, error) {
	return func(context.Context, string, []string, string) ([]byte, error) {
		return []byte(out), err
	}
}

func TestJudgeDirectVerdict(t *testing.T) {
	r := New("agente").withRunCLI(fakeCLI(`{"relation":"conflicts_with","confidence":0.9,"reasoning":"se contradicen"}`, nil))
	v, err := r.Judge(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Judge error: %v", err)
	}
	if v.Relation != domain.RelationConflictsWith || v.Confidence != 0.9 {
		t.Errorf("veredicto mal parseado: %+v", v)
	}
}

func TestJudgeEnvelopeWrapped(t *testing.T) {
	// Sobre tipo claude: el veredicto va dentro de "result".
	out := `{"result":"{\"Relation\":\"supersedes\",\"Confidence\":0.8,\"Reasoning\":\"A reemplaza B\"}","modelUsage":{"haiku":{}}}`
	r := New("claude").withRunCLI(fakeCLI(out, nil))
	v, err := r.Judge(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Judge error: %v", err)
	}
	if v.Relation != domain.RelationSupersedes {
		t.Errorf("relación = %q, se esperaba supersedes", v.Relation)
	}
	if v.Model != "haiku" {
		t.Errorf("modelo = %q, se esperaba haiku (de modelUsage)", v.Model)
	}
}

func TestJudgeStripsCodeFences(t *testing.T) {
	out := "```json\n{\"relation\":\"related\",\"confidence\":0.5,\"reasoning\":\"mismo tema\"}\n```"
	r := New("agente").withRunCLI(fakeCLI(out, nil))
	v, err := r.Judge(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Judge error: %v", err)
	}
	if v.Relation != domain.RelationRelated {
		t.Errorf("relación = %q, se esperaba related", v.Relation)
	}
}

func TestJudgeUnknownRelationFails(t *testing.T) {
	r := New("agente").withRunCLI(fakeCLI(`{"relation":"inventada","confidence":1}`, nil))
	_, err := r.Judge(context.Background(), "prompt")
	if !errors.Is(err, ErrUnknownRelation) {
		t.Fatalf("se esperaba ErrUnknownRelation, se obtuvo %v", err)
	}
}

func TestJudgePendingIsRejected(t *testing.T) {
	r := New("agente").withRunCLI(fakeCLI(`{"relation":"pending","confidence":1}`, nil))
	_, err := r.Judge(context.Background(), "prompt")
	if !errors.Is(err, ErrUnknownRelation) {
		t.Fatalf("pending no es un veredicto válido; se esperaba ErrUnknownRelation, se obtuvo %v", err)
	}
}

func TestJudgeBadJSONFails(t *testing.T) {
	r := New("agente").withRunCLI(fakeCLI(`no es json`, nil))
	_, err := r.Judge(context.Background(), "prompt")
	if !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("se esperaba ErrInvalidJSON, se obtuvo %v", err)
	}
}

func TestJudgePropagatesCLIError(t *testing.T) {
	r := New("agente").withRunCLI(fakeCLI("", ErrCLINotInstalled))
	_, err := r.Judge(context.Background(), "prompt")
	if !errors.Is(err, ErrCLINotInstalled) {
		t.Fatalf("debe propagar el error del shell-out: %v", err)
	}
}
