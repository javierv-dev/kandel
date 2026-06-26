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
	project := fs.String("project", "", "proyecto por defecto del proceso (si no, KANDEL_PROJECT)")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel mcp [--tools agent|admin|all|<lista>] [--project P]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	svc := mcpadapter.Services{
		Memory:         app.Memory,
		Session:        app.Session,
		Lifecycle:      app.Lifecycle,
		Prompt:         app.Prompt,
		Doctor:         app.Doctor,
		Conflict:       app.Conflict,
		DefaultProject: resolveProject(*project),
	}
	srv := mcpadapter.NewServer(svc, mcpadapter.ResolveTools(*tools))
	// ServeStdio bloquea leyendo stdin hasta que el cliente cierra la conexión.
	return server.ServeStdio(srv)
}
