package cli

import (
	"context"
	"flag"
	"fmt"
)

func init() {
	register(pendingCmd{})
}

// pendingCmd lista las memorias pendientes por resolver (resolution = open).
type pendingCmd struct{}

func (pendingCmd) Name() string     { return "pending" }
func (pendingCmd) Synopsis() string { return "lista memorias pendientes por resolver" }

func (pendingCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("pending", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	project := fs.String("project", "", "proyecto (por defecto KANDEL_PROJECT)")
	limit := fs.Int("limit", 20, "número máximo de memorias")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel pending [--project P] [--limit N]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	memories, err := app.Lifecycle.PendingResolution(ctx, resolveProject(*project), *limit)
	if err != nil {
		return err
	}
	if len(memories) == 0 {
		fmt.Fprintln(app.Out, "ninguna memoria pendiente")
		return nil
	}
	for _, m := range memories {
		fmt.Fprintf(app.Out, "#%d  [%s]  %s\n", m.ID, m.Type, m.Title)
	}
	return nil
}
