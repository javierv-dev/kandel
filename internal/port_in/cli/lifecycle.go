package cli

import (
	"context"
	"flag"
	"fmt"
)

func init() {
	register(reviewCmd{})
}

// reviewCmd lista las memorias cuya fecha de revisión ya venció (decaimiento).
type reviewCmd struct{}

func (reviewCmd) Name() string     { return "review" }
func (reviewCmd) Synopsis() string { return "lista memorias que piden revisión (decaimiento)" }

func (reviewCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	project := fs.String("project", "", "proyecto (por defecto KANDEL_PROJECT)")
	limit := fs.Int("limit", 20, "número máximo de memorias")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel review [--project P] [--limit N]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	memories, err := app.Lifecycle.NeedingReview(ctx, resolveProject(*project), *limit)
	if err != nil {
		return err
	}
	if len(memories) == 0 {
		fmt.Fprintln(app.Out, "ninguna memoria pide revisión")
		return nil
	}
	for _, m := range memories {
		fmt.Fprintf(app.Out, "#%d  [%s]  %s\n", m.ID, m.Type, m.Title)
	}
	return nil
}
