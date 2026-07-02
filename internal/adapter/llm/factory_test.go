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
