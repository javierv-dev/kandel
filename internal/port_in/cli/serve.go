package cli

import (
	"context"
	"flag"
	"fmt"
	nethttp "net/http"
	"os"
	"strings"

	httpadapter "github.com/javierv-dev/kandel/internal/port_in/http"
)

// defaultPort es el puerto por defecto del daemon HTTP. KANDEL_PORT lo sobreescribe; el
// instalador de cada agente lo fija de forma coherente en los hooks y aquí (ver §18 del plan).
const defaultPort = "8765"

// resolvePort devuelve el puerto efectivo del daemon: KANDEL_PORT si está definido, si no el
// valor por defecto. Mantiene un único origen de verdad para el puerto entre hooks y servidor.
func resolvePort() string {
	if p := strings.TrimSpace(os.Getenv("KANDEL_PORT")); p != "" {
		return p
	}
	return defaultPort
}

// serveCmd arranca la API HTTP local. Expone los casos de uso como endpoints REST para
// integraciones (paneles, scripts) que prefieran HTTP al MCP.
type serveCmd struct{}

func init() { register(serveCmd{}) }

func (serveCmd) Name() string     { return "serve" }
func (serveCmd) Synopsis() string { return "arranca la API HTTP local" }

func (serveCmd) Run(_ context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	// Por defecto vacío: si no se pasa --addr, se compone desde KANDEL_PORT en 127.0.0.1.
	addr := fs.String("addr", "", "dirección donde escuchar (por defecto 127.0.0.1:$KANDEL_PORT)")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel serve [--addr 127.0.0.1:8765]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*addr) == "" {
		*addr = "127.0.0.1:" + resolvePort()
	}

	handler := httpadapter.NewHandler(httpadapter.Services{
		Memory:    app.Memory,
		Session:   app.Session,
		Lifecycle: app.Lifecycle,
		Prompt:    app.Prompt,
		Context:   app.Context,
		Project:   app.Project,
	})
	fmt.Fprintf(app.Out, "kandel HTTP escuchando en http://%s\n", *addr)
	// ListenAndServe bloquea hasta que el proceso termina o falla.
	return nethttp.ListenAndServe(*addr, handler)
}
