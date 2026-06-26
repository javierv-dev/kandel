package cli

import (
	"context"
	"flag"
	"fmt"
	"strconv"
)

// pinCmd fija una memoria arriba en el contexto (estado local, no se sincroniza).
type pinCmd struct{}

func init() {
	register(pinCmd{})
	register(unpinCmd{})
	register(reviewCmd{})
}

func (pinCmd) Name() string     { return "pin" }
func (pinCmd) Synopsis() string { return "fija una memoria arriba en el contexto" }

func (pinCmd) Run(ctx context.Context, args []string, app *App) error {
	id, err := singleID("pin", args, app)
	if err != nil {
		return err
	}
	if err := app.Lifecycle.Pin(ctx, id); err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "fijada memoria #%d\n", id)
	return nil
}

// unpinCmd desfija una memoria.
type unpinCmd struct{}

func (unpinCmd) Name() string     { return "unpin" }
func (unpinCmd) Synopsis() string { return "desfija una memoria" }

func (unpinCmd) Run(ctx context.Context, args []string, app *App) error {
	id, err := singleID("unpin", args, app)
	if err != nil {
		return err
	}
	if err := app.Lifecycle.Unpin(ctx, id); err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "desfijada memoria #%d\n", id)
	return nil
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

// singleID parsea un único id posicional, compartido por pin y unpin.
func singleID(name string, args []string, app *App) (int64, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(app.Err)
	fs.Usage = func() { fmt.Fprintf(app.Err, "uso: kandel %s <id>\n", name) }
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return 0, fmt.Errorf("%s: falta el id", name)
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: id inválido %q", name, fs.Arg(0))
	}
	return id, nil
}
