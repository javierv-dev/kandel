package llm

import (
	"context"
	"os"
	"strings"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// FromEnv elige el juez LLM según la variable KANDEL_AGENT_CLI:
//   - "claude"            → CLI de Claude (claude -p --output-format json --model haiku --max-turns 1)
//   - cualquier otro valor → se trata como el nombre de un comando que recibe el prompt por stdin
//     y devuelve el veredicto en JSON por stdout
//   - vacío               → un runner noop que falla en Judge (no hay agente configurado)
//
// Siempre devuelve un AgentRunner usable: así el ConflictService se construye igual y la
// detección de candidatos (que no usa el juez) y el registro de veredictos del agente
// (mem_judge) funcionan aunque no haya CLI de agente; solo mem_compare fallaría, con mensaje claro.
func FromEnv() ports.AgentRunner {
	switch agent := strings.TrimSpace(os.Getenv("KANDEL_AGENT_CLI")); agent {
	case "":
		return Noop{}
	case "claude":
		return New("claude", "-p", "--output-format", "json", "--model", "haiku", "--max-turns", "1")
	default:
		// Comando personalizado: se le pasa el prompt por stdin y se espera JSON por stdout.
		return New(agent)
	}
}

// defaultSeedModel es el modelo que usa el seed cuando no se especifica uno. El seed razona sobre
// prosa larga (documentación del proyecto), más exigente que el juez de conflictos (que usa haiku),
// por lo que conviene un modelo más capaz por defecto.
const defaultSeedModel = "sonnet"

// Generator construye un AgentGenerator para generación libre (lo usa el seed). A diferencia de
// FromEnv (que fija el modelo barato del juez), aquí el agente y el modelo son explícitos: el
// instalador sabe qué CLI de agente corresponde (claude, gemini, …) y el seed razona mejor con un
// modelo más capaz. Reglas:
//   - "claude"             → claude -p --output-format json --model <model> --max-turns 1
//   - cualquier otro valor → se trata como el nombre de un comando que recibe el prompt por stdin y
//     devuelve la salida por stdout
//   - vacío                → un Noop cuyo Generate falla con ErrCLINotInstalled (no hay agente)
func Generator(agent, model string) ports.AgentGenerator {
	switch a := strings.TrimSpace(agent); a {
	case "":
		return Noop{}
	case "claude":
		if strings.TrimSpace(model) == "" {
			model = defaultSeedModel
		}
		return New("claude", "-p", "--output-format", "json", "--model", model, "--max-turns", "1")
	default:
		return New(a)
	}
}

// Noop es un AgentRunner/AgentGenerator que no hace nada: devuelve ErrCLINotInstalled. Se usa cuando
// no hay agente configurado, para que el resto del sistema funcione y la operación que sí necesita el
// agente (mem_compare, seed) falle con un mensaje útil.
type Noop struct{}

var (
	_ ports.AgentRunner    = Noop{}
	_ ports.AgentGenerator = Noop{}
)

// Judge siempre falla indicando que no hay agente configurado.
func (Noop) Judge(context.Context, string) (domain.Verdict, error) {
	return domain.Verdict{}, ErrCLINotInstalled
}

// Generate siempre falla indicando que no hay agente configurado.
func (Noop) Generate(context.Context, string) (string, error) {
	return "", ErrCLINotInstalled
}
