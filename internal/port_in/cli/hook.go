package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/javierv-dev/kandel/internal/port_in/hook"
)

// hookCmd ejecuta un hook de integración con el agente. Es de uso interno: los scripts que escribe
// el instalador (ver internal/setup) son wrappers de una línea que llaman a `kandel hook <evento>`.
// Toda la lógica del hook vive en el binario (paquete hook) para no depender de jq/curl en el shell.
type hookCmd struct{}

func init() { register(hookCmd{}) }

func (hookCmd) Name() string { return "hook" }
func (hookCmd) Synopsis() string {
	return "ejecuta un hook de integración (uso interno del instalador)"
}

func (hookCmd) Run(ctx context.Context, args []string, app *App) error {
	if len(args) == 0 {
		fmt.Fprintln(app.Err, "uso: kandel hook <session-start|post-compaction|user-prompt-submit|session-stop|subagent-stop>")
		return nil
	}
	// Un hook NUNCA debe terminar con error: un exit≠0 puede bloquear el mensaje del usuario en el
	// agente. Cualquier fallo se reporta por stderr (no bloquea) y se devuelve nil.
	if err := hook.Run(ctx, args[0], app.Version, app.Update, os.Stdin, app.Out); err != nil {
		fmt.Fprintln(app.Err, "kandel hook:", err)
	}
	return nil
}
