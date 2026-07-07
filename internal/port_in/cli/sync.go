package cli

import (
	"context"
	"flag"
	"fmt"
)

// syncCmd sincroniza la memoria contra el hub (modelo change-feed). Sin flags hace push + pull: sube
// lo local pendiente y baja lo ajeno en un solo comando.
type syncCmd struct{}

func init() { register(syncCmd{}) }

func (syncCmd) Name() string     { return "sync" }
func (syncCmd) Synopsis() string { return "sincroniza la memoria con el hub (push + pull)" }

func (syncCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel sync")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	push, err := app.Sync.Push(ctx)
	if err != nil {
		return err
	}
	pull, err := app.Sync.Pull(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "sincronizado: %d subida(s), %d bajada(s)\n", push.Count, pull.Applied)
	return nil
}
