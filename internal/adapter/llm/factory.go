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

// Noop es un AgentRunner que no juzga: devuelve ErrCLINotInstalled. Se usa cuando no hay agente
// configurado, para que el resto del sistema funcione y mem_compare falle con un mensaje útil.
type Noop struct{}

var _ ports.AgentRunner = Noop{}

// Judge siempre falla indicando que no hay agente configurado.
func (Noop) Judge(context.Context, string) (domain.Verdict, error) {
	return domain.Verdict{}, ErrCLINotInstalled
}
