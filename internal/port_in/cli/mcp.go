package cli

import (
	"context"
	"flag"
	"fmt"

	mcpadapter "github.com/javierv-dev/kandel/internal/port_in/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// mcpCmd arranca el servidor MCP por stdio. Expone las herramientas mem_* a cualquier agente
// que añada kandel como servidor MCP. El flag --tools acota qué herramientas se registran
// (perfiles "agent"/"admin" o nombres sueltos); vacío = todas.
type mcpCmd struct{}

func init() { register(mcpCmd{}) }

func (mcpCmd) Name() string     { return "mcp" }
func (mcpCmd) Synopsis() string { return "arranca el servidor MCP por stdio (tools mem_*)" }

func (mcpCmd) Run(_ context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	tools := fs.String("tools", "", "perfiles o herramientas a registrar (agent|admin|all|mem_save,...)")
	source := fs.String("source", "", "aplicación de esta instalación MCP (claude-code|antigravity|pi)")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel mcp [--tools agent|admin|all|<lista>]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *source != "" && *source != "claude-code" && *source != "antigravity" && *source != "pi" {
		return fmt.Errorf("--source debe ser claude-code, antigravity o pi")
	}
	svc := mcpadapter.Services{
		Source:    *source,
		Memory:    app.Memory,
		Lifecycle: app.Lifecycle,
		Prompt:    app.Prompt,
		Doctor:    app.Doctor,
		Conflict:  app.Conflict,
		Sync:      app.Sync,
		Thread:    app.Thread,
		Handoff:   app.Handoff,
		Sessions:  app.SessionLookup,
	}
	srv := mcpadapter.NewServer(svc, mcpadapter.ResolveTools(*tools))
	// ServeStdio bloquea leyendo stdin hasta que el cliente cierra la conexión.
	return server.ServeStdio(srv)
}
