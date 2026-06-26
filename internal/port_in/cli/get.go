package cli

import (
	"context"
	"flag"
	"fmt"
	"strconv"

	"github.com/javierv-dev/kandel/internal/domain"
)

// getCmd muestra una memoria por su id. Lectura directa.
type getCmd struct{}

func init() { register(getCmd{}) }

func (getCmd) Name() string     { return "get" }
func (getCmd) Synopsis() string { return "muestra una memoria por id" }

func (getCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	fs.Usage = func() { fmt.Fprintln(app.Err, "uso: kandel get <id>") }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return fmt.Errorf("get: falta el id")
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil {
		return fmt.Errorf("get: id inválido %q", fs.Arg(0))
	}

	m, err := app.Memory.Get(ctx, id)
	if err != nil {
		return err
	}
	if m == nil {
		return domain.ErrNotFound
	}
	fmt.Fprintf(app.Out, "#%d  [%s/%s]  %s\n", m.ID, m.Type, m.Scope, m.Title)
	if m.TopicKey != "" {
		fmt.Fprintf(app.Out, "topic: %s\n", m.TopicKey)
	}
	fmt.Fprintf(app.Out, "proyecto: %s\n\n%s\n", m.Project, m.Content)
	return nil
}
