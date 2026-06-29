package ports

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
)

// AgentRunner es el juez LLM: recibe un prompt ya construido (con las dos memorias a comparar)
// y devuelve un veredicto estructurado sobre su relación semántica. La etapa cara del embudo
// de conflictos (vecinos baratos → juicio caro solo sobre ellos). Es shell-out a un CLI de
// agente; por eso recibe context para cancelación y timeout.
type AgentRunner interface {
	// Judge compara dos memorias descritas en el prompt y emite el veredicto.
	// En error el Verdict devuelto es el valor cero.
	Judge(ctx context.Context, prompt string) (domain.Verdict, error)
}

// AgentGenerator es generación libre por LLM: recibe un prompt y devuelve la salida de texto del
// agente, sin parsearla como veredicto. La usa el seed (poblar la memoria de un proyecto existente)
// para pedirle al agente que extraiga decisiones de alto valor de la documentación. Como Judge, es
// shell-out a un CLI de agente; recibe context para cancelación y timeout.
type AgentGenerator interface {
	// Generate envía el prompt al agente y devuelve su salida de texto (ya desenvuelta del sobre
	// de las CLIs que lo usan, p. ej. claude --output-format json). En error devuelve "".
	Generate(ctx context.Context, prompt string) (string, error)
}
