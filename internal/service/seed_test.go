package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// seedFakeGenerator devuelve una salida fija y registra el prompt recibido.
type seedFakeGenerator struct {
	out    string
	err    error
	prompt string
}

func (g *seedFakeGenerator) Generate(_ context.Context, prompt string) (string, error) {
	g.prompt = prompt
	return g.out, g.err
}

// seedFakeSaver registra las entradas guardadas y devuelve un desenlace configurable.
type seedFakeSaver struct {
	saved   []SaveInput
	outcome SaveOutcome
	err     error
}

func (s *seedFakeSaver) Save(_ context.Context, in SaveInput) (SaveResult, error) {
	s.saved = append(s.saved, in)
	if s.err != nil {
		return SaveResult{}, s.err
	}
	oc := s.outcome
	if oc == "" {
		oc = SaveCreated
	}
	return SaveResult{Memory: domain.Memory{ID: int64(len(s.saved)), Title: in.Title}, Outcome: oc}, nil
}

// genFactory devuelve una fábrica que siempre entrega el generador dado.
func genFactory(g ports.AgentGenerator) func(string, string) ports.AgentGenerator {
	return func(string, string) ports.AgentGenerator { return g }
}

func TestSeedGenerateParsesAndFiltersCandidates(t *testing.T) {
	gen := &seedFakeGenerator{out: `[
		{"type":"decision","scope":"project","title":"Motor de BD","content":"**What** Postgres"},
		{"type":"discovery","scope":"project","title":"trivial","content":"se descarta por tipo"},
		{"type":"architecture","title":"Hexagonal","content":"capas domain/ports/service"},
		{"type":"discovery","scope":"project","title":"","content":"sin título, se descarta"}
	]`}
	svc := NewSeedService(&seedFakeSaver{}, genFactory(gen))

	cands, err := svc.Generate(context.Background(), SeedGenerateInput{Sources: "docs", Limit: 10})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("se esperaban 2 candidatos válidos, hay %d: %+v", len(cands), cands)
	}
	if cands[0].Type != "decision" || cands[1].Type != "architecture" {
		t.Errorf("tipos inesperados: %+v", cands)
	}
	// El alcance ausente cae a project.
	if cands[1].Scope != string(domain.ScopeProject) {
		t.Errorf("alcance por defecto = %q, se esperaba project", cands[1].Scope)
	}
	// El prompt debe incluir la documentación.
	if !strings.Contains(gen.prompt, "docs") {
		t.Errorf("el prompt no incluye las fuentes")
	}
}

func TestSeedGenerateStripsCodeFence(t *testing.T) {
	gen := &seedFakeGenerator{out: "```json\n[{\"type\":\"decision\",\"scope\":\"project\",\"title\":\"X\",\"content\":\"Y\"}]\n```"}
	svc := NewSeedService(&seedFakeSaver{}, genFactory(gen))
	cands, err := svc.Generate(context.Background(), SeedGenerateInput{Sources: "docs"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("se esperaba 1 candidato, hay %d", len(cands))
	}
}

func TestSeedGenerateRespectsLimit(t *testing.T) {
	gen := &seedFakeGenerator{out: `[
		{"type":"decision","scope":"project","title":"A","content":"a"},
		{"type":"decision","scope":"project","title":"B","content":"b"}
	]`}
	svc := NewSeedService(&seedFakeSaver{}, genFactory(gen))
	cands, err := svc.Generate(context.Background(), SeedGenerateInput{Sources: "docs", Limit: 1})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("el límite no se respetó: %d candidatos", len(cands))
	}
}

func TestSeedGenerateUnboundedReturnsAll(t *testing.T) {
	gen := &seedFakeGenerator{out: `[
		{"type":"decision","scope":"project","title":"A","content":"a"},
		{"type":"decision","scope":"project","title":"B","content":"b"},
		{"type":"decision","scope":"project","title":"C","content":"c"}
	]`}
	svc := NewSeedService(&seedFakeSaver{}, genFactory(gen))
	// Limit 0 = sin tope: devuelve todos los candidatos válidos.
	cands, err := svc.Generate(context.Background(), SeedGenerateInput{Sources: "docs", Limit: 0})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(cands) != 3 {
		t.Fatalf("sin tope debe devolver los 3 candidatos, devolvió %d", len(cands))
	}
	// Sin tope, el prompt NO debe inyectar un máximo numérico.
	if strings.Contains(gen.prompt, "MÁXIMO DE ELEMENTOS") {
		t.Errorf("con Limit 0 el prompt no debe fijar un máximo: %q", gen.prompt)
	}
}

func TestSeedGenerateEmptySourcesFails(t *testing.T) {
	svc := NewSeedService(&seedFakeSaver{}, genFactory(&seedFakeGenerator{}))
	if _, err := svc.Generate(context.Background(), SeedGenerateInput{Sources: "  "}); err == nil {
		t.Fatal("se esperaba error por falta de documentación")
	}
}

func TestSeedGeneratePropagatesGeneratorError(t *testing.T) {
	gen := &seedFakeGenerator{err: errors.New("cli caída")}
	svc := NewSeedService(&seedFakeSaver{}, genFactory(gen))
	if _, err := svc.Generate(context.Background(), SeedGenerateInput{Sources: "docs"}); err == nil {
		t.Fatal("se esperaba error del generador")
	}
}

func TestSeedSaveBatch(t *testing.T) {
	saver := &seedFakeSaver{outcome: SaveCreated}
	svc := NewSeedService(saver, genFactory(&seedFakeGenerator{}))
	cands := []SeedCandidate{
		{Type: "decision", Scope: "project", Title: "A", Content: "a"},
		{Type: "architecture", Scope: "project", Title: "B", Content: "b"},
	}
	res, err := svc.Save(context.Background(), cands, "kandel")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if res.Created != 2 || res.Failed != 0 {
		t.Fatalf("conteos inesperados: %+v", res)
	}
	if len(saver.saved) != 2 {
		t.Fatalf("se esperaban 2 guardados, hay %d", len(saver.saved))
	}
	for _, in := range saver.saved {
		if in.ToolName != seedToolName {
			t.Errorf("procedencia = %q, se esperaba %q", in.ToolName, seedToolName)
		}
		if in.Project != "kandel" {
			t.Errorf("proyecto = %q, se esperaba kandel", in.Project)
		}
	}
}

func TestSeedSaveCountsInvalidScopeAsFailed(t *testing.T) {
	saver := &seedFakeSaver{}
	svc := NewSeedService(saver, genFactory(&seedFakeGenerator{}))
	cands := []SeedCandidate{{Type: "decision", Scope: "inexistente", Title: "A", Content: "a"}}
	res, err := svc.Save(context.Background(), cands, "kandel")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if res.Failed != 1 || res.Created != 0 {
		t.Fatalf("un alcance inválido debe contar como fallido: %+v", res)
	}
}
