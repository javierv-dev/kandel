package llm

import (
	"context"
	"errors"
	"testing"
)

// TestFromEnvUnsupportedAgent verifica que un agente nombrado pero sin contrato de CLI implementado
// (p. ej. opencode) NO se lanza a ciegas: FromEnv devuelve un runner que falla con ErrAgentUnsupported.
func TestFromEnvUnsupportedAgent(t *testing.T) {
	t.Setenv("KANDEL_AGENT_CLI", "opencode")
	r := FromEnv()
	_, err := r.Judge(context.Background(), "prompt")
	if !errors.Is(err, ErrAgentUnsupported) {
		t.Fatalf("se esperaba ErrAgentUnsupported, se obtuvo: %v", err)
	}
}

// TestFromEnvEmptyAgent confirma que sin KANDEL_AGENT_CLI el runner es Noop (falla con
// ErrCLINotInstalled, no con ErrAgentUnsupported): "vacío" y "no soportado" son casos distintos.
func TestFromEnvEmptyAgent(t *testing.T) {
	t.Setenv("KANDEL_AGENT_CLI", "")
	t.Setenv("KANDEL_DATA_DIR", t.TempDir()) // aísla del ~/.kandel/config real (fallback de config)
	r := FromEnv()
	_, err := r.Judge(context.Background(), "prompt")
	if !errors.Is(err, ErrCLINotInstalled) {
		t.Fatalf("se esperaba ErrCLINotInstalled (Noop), se obtuvo: %v", err)
	}
}

// TestGeneratorUnsupportedAgent verifica el mismo aviso en el camino del seed: un agente no
// soportado devuelve un generador que falla con ErrAgentUnsupported en vez de ejecutar un comando.
func TestGeneratorUnsupportedAgent(t *testing.T) {
	g := Generator("opencode", "")
	_, err := g.Generate(context.Background(), "prompt")
	if !errors.Is(err, ErrAgentUnsupported) {
		t.Fatalf("se esperaba ErrAgentUnsupported, se obtuvo: %v", err)
	}
}

// TestModelRegistry verifica que la allowlist y los modelos se derivan del registro único
// modelsByAgent: claude y pi están soportados; un agente ausente no lo está y su seed es "".
func TestModelRegistry(t *testing.T) {
	if !IsSupported("claude") {
		t.Error("claude debe estar soportado (está en modelsByAgent)")
	}
	if !IsSupported("pi") {
		t.Error("pi debe estar soportado (está en modelsByAgent)")
	}
	if IsSupported("gemini") {
		t.Error("gemini aún no debe estar soportado")
	}
	if got := SeedModelFor("claude"); got != "sonnet" {
		t.Errorf("SeedModelFor(claude) = %q, se esperaba sonnet", got)
	}
	if got := SeedModelFor("pi"); got != "gemini-4-hydrogen" {
		t.Errorf("SeedModelFor(pi) = %q, se esperaba gemini-4-hydrogen", got)
	}
	if got := SeedModelFor("gemini"); got != "" {
		t.Errorf("SeedModelFor de agente ausente = %q, se esperaba vacío", got)
	}
	if sup := SupportedAgents(); len(sup) != 2 || sup[0] != "claude" || sup[1] != "pi" {
		t.Errorf("SupportedAgents = %v, se esperaba [claude pi]", sup)
	}
}

func TestFromEnvAndGeneratorPi(t *testing.T) {
	t.Setenv("KANDEL_AGENT_CLI", "pi")
	r, ok := FromEnv().(*Runner)
	if !ok {
		t.Fatalf("FromEnv con pi debe devolver *Runner, obtuvo %T", FromEnv())
	}
	if r.name != "pi" {
		t.Errorf("Runner.name = %q, se esperaba pi", r.name)
	}
	if len(r.args) == 0 || r.args[len(r.args)-1] != "gemini-3-8-flash" {
		t.Errorf("Runner.args = %v, se esperaba modelo judge gemini-3-8-flash", r.args)
	}

	g, ok := Generator("pi", "").(*Runner)
	if !ok {
		t.Fatalf("Generator(pi) debe devolver *Runner, obtuvo %T", Generator("pi", ""))
	}
	if g.name != "pi" || g.args[len(g.args)-1] != "gemini-4-hydrogen" {
		t.Errorf("Generator(pi) = %s %v, se esperaba gemini-4-hydrogen", g.name, g.args)
	}

	gCustom, ok := Generator("pi", "custom-model").(*Runner)
	if !ok || gCustom.args[len(gCustom.args)-1] != "custom-model" {
		t.Errorf("Generator(pi, custom-model) args = %v", gCustom.args)
	}
}
