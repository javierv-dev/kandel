package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/javierv-dev/kandel/internal/service"
)

// sessionCmd gestiona las sesiones del agente con subcomandos: start, end, current.
type sessionCmd struct{}

func init() { register(sessionCmd{}) }

func (sessionCmd) Name() string     { return "session" }
func (sessionCmd) Synopsis() string { return "gestiona sesiones (start|end|current)" }

func (sessionCmd) Run(ctx context.Context, args []string, app *App) error {
	if len(args) == 0 {
		fmt.Fprintln(app.Err, "uso: kandel session <start|end|current> [argumentos]")
		return fmt.Errorf("session: falta el subcomando")
	}
	switch args[0] {
	case "start":
		return sessionStart(ctx, args[1:], app)
	case "end":
		return sessionEnd(ctx, args[1:], app)
	case "current":
		return sessionCurrent(ctx, args[1:], app)
	default:
		fmt.Fprintln(app.Err, "uso: kandel session <start|end|current> [argumentos]")
		return fmt.Errorf("session: subcomando desconocido %q", args[0])
	}
}

// sessionStart abre una sesión nueva.
func sessionStart(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("session start", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	id := fs.String("id", "", "id de la sesión (obligatorio)")
	project := fs.String("project", "", "proyecto (por defecto: detección git del directorio)")
	dir := fs.String("dir", "", "directorio de trabajo")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("session start: --id es obligatorio")
	}
	s, err := app.Session.Start(ctx, service.StartInput{
		ID:        *id,
		Project:   resolveProject(*project),
		Directory: *dir,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "sesión iniciada: %s (%s)\n", s.ID, s.Project)
	return nil
}

// sessionEnd cierra una sesión (marca su instante de cierre).
func sessionEnd(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("session end", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("session end: falta el id")
	}
	if err := app.Session.End(ctx, fs.Arg(0)); err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "sesión cerrada: %s\n", fs.Arg(0))
	return nil
}

// sessionCurrent muestra la sesión abierta más reciente del proyecto.
func sessionCurrent(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("session current", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	project := fs.String("project", "", "proyecto (por defecto: detección git del directorio)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	id, ok, err := app.Session.MostRecentActive(ctx, resolveProject(*project))
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(app.Out, "no hay ninguna sesión abierta")
		return nil
	}
	fmt.Fprintf(app.Out, "sesión activa: %s\n", id)
	return nil
}
