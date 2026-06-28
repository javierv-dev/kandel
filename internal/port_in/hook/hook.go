// Package hook es el adaptador de entrada de los hooks de ciclo de vida de los agentes (hoy
// Claude Code). Toda la lógica del hook vive aquí, en Go, en lugar de en scripts shell: así la
// integración no depende de herramientas externas como jq (que no viene en Windows) y el binario
// kandel puede dar un seguimiento exacto de cada evento.
//
// Cada hook recibe un JSON por stdin (lo envía el agente), habla con el daemon HTTP local
// (kandel serve) y, cuando corresponde, imprime contexto por stdout para que el agente lo inyecte.
// Los scripts instalados quedan como wrappers de una línea: `exec kandel hook <evento>`.
package hook

import (
	"context"
	"fmt"
	"io"
)

// runner agrupa el estado común de un hook: el cliente HTTP del daemon, el destino de salida
// (stdout del proceso, que el agente interpreta como contexto) y el nombre del evento (para el log).
type runner struct {
	event  string
	client *client
	out    io.Writer
	ctx    context.Context
}

// Run ejecuta el hook indicado por event, leyendo el JSON del agente de stdin y escribiendo la
// salida para el agente en out. NUNCA debe devolver error para eventos válidos: un fallo del hook
// (exit≠0) puede bloquear el mensaje del usuario en el agente. Solo devuelve error ante un evento
// desconocido (error de configuración del instalador, no del flujo normal).
func Run(ctx context.Context, event string, stdin io.Reader, out io.Writer) error {
	r := &runner{event: event, client: newClient(), out: out, ctx: ctx}
	payload := parsePayload(stdin)

	switch event {
	case "session-start":
		return r.sessionStart(payload)
	case "post-compaction":
		return r.postCompaction(payload)
	case "user-prompt-submit":
		return r.userPromptSubmit(payload)
	case "session-stop":
		return r.sessionStop(payload)
	case "subagent-stop":
		return r.subagentStop(payload)
	default:
		return fmt.Errorf("hook desconocido: %q", event)
	}
}
