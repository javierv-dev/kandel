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
