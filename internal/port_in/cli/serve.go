package cli

import (
	"context"
	"flag"
	"fmt"
	nethttp "net/http"

	httpadapter "github.com/javierv-dev/kandel/internal/port_in/http"
)

// serveCmd arranca la API HTTP local. Expone los casos de uso como endpoints REST para
// integraciones (paneles, scripts) que prefieran HTTP al MCP.
type serveCmd struct{}

func init() { register(serveCmd{}) }

func (serveCmd) Name() string     { return "serve" }
func (serveCmd) Synopsis() string { return "arranca la API HTTP local" }

func (serveCmd) Run(_ context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	addr := fs.String("addr", "127.0.0.1:8765", "dirección donde escuchar")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel serve [--addr 127.0.0.1:8765]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	handler := httpadapter.NewHandler(httpadapter.Services{
		Memory:    app.Memory,
		Session:   app.Session,
		Lifecycle: app.Lifecycle,
		Prompt:    app.Prompt,
	})
	fmt.Fprintf(app.Out, "kandel HTTP escuchando en http://%s\n", *addr)
	// ListenAndServe bloquea hasta que el proceso termina o falla.
	return nethttp.ListenAndServe(*addr, handler)
}
