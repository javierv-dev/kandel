package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/javierv-dev/kandel/internal/config"
)

// alertsCmd permite inspeccionar y dar de baja (ocultar o reiniciar) los eventos/avisos del sistema
// registrados en ~/.kandel/alerts.json (por ejemplo, cuando el juez LLM falla en segundo plano).
type alertsCmd struct{}

func init() { register(alertsCmd{}) }

func (alertsCmd) Name() string { return "alerts" }
func (alertsCmd) Synopsis() string {
	return "lista o da de baja avisos y eventos del sistema (list|dismiss|clear)"
}

func (alertsCmd) Run(_ context.Context, args []string, app *App) error {
	dir, err := config.DefaultDir()
	if err != nil {
		return err
	}
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "list":
		return alertsList(app, dir)
	case "dismiss":
		return alertsDismiss(app, dir, args[1:], false)
	case "clear", "reset":
		return alertsDismiss(app, dir, args[1:], true)
	default:
		return fmt.Errorf("subcomando desconocido: %s (usa list|dismiss|clear)", sub)
	}
}

func alertsList(app *App, dir string) error {
	alerts, err := config.LoadAlerts(dir)
	if err != nil {
		return err
	}
	if len(alerts) == 0 {
		fmt.Fprintln(app.Out, "no hay eventos ni alertas registrados.")
		return nil
	}
	for _, a := range alerts {
		state := "activo"
		if a.Paused {
			state = "pausado (límite de intentos alcanzado)"
		}
		if a.Dismissed {
			state += ", aviso ocultado"
		}
		fmt.Fprintf(app.Out, "- [%s] agente=%s modelo=%s fallos=%d/%d (%s)\n",
			a.Kind, a.Agent, a.Model, a.Failures, a.MaxAttempts, state)
		if a.LastError != "" {
			fmt.Fprintf(app.Out, "  motivo: %s\n", a.LastError)
		}
	}
	return nil
}

func alertsDismiss(app *App, dir string, args []string, reset bool) error {
	fs := flag.NewFlagSet("alerts dismiss", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	resetFlag := fs.Bool("reset", reset, "además de ocultar el aviso, borra el contador para reanudar los intentos")
	if err := fs.Parse(args); err != nil {
		return err
	}
	kind := "all"
	if fs.NArg() > 0 {
		kind = strings.TrimSpace(fs.Arg(0))
	}
	if err := config.DismissAlert(dir, kind, *resetFlag); err != nil {
		return err
	}
	if *resetFlag {
		fmt.Fprintf(app.Out, "alertas reiniciadas (%s): el juez volverá a intentarlo en el próximo barrido.\n", kind)
	} else {
		fmt.Fprintf(app.Out, "aviso ocultado (%s).\n", kind)
	}
	return nil
}
