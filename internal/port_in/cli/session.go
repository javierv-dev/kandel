package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/javierv-dev/kandel/internal/service"
)

// sessionCmd gestiona las sesiones del agente con subcomandos: start, end.
type sessionCmd struct{}

func init() { register(sessionCmd{}) }

func (sessionCmd) Name() string     { return "session" }
func (sessionCmd) Synopsis() string { return "gestiona sesiones (start|end)" }

func (sessionCmd) Run(ctx context.Context, args []string, app *App) error {
	if len(args) == 0 {
		fmt.Fprintln(app.Err, "uso: kandel session <start|end> [argumentos]")
		return fmt.Errorf("session: falta el subcomando")
	}
	switch args[0] {
	case "start":
		return sessionStart(ctx, args[1:], app)
	case "end":
		return sessionEnd(ctx, args[1:], app)
	default:
		fmt.Fprintln(app.Err, "uso: kandel session <start|end> [argumentos]")
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
