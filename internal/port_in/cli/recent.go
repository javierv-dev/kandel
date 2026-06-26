package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// recentCmd lista memorias recientes. Es una lectura directa (navegación del usuario): no
// pasa por el embedder, así que funciona aunque Ollama esté caído.
type recentCmd struct{}

func init() { register(recentCmd{}) }

func (recentCmd) Name() string     { return "recent" }
func (recentCmd) Synopsis() string { return "lista memorias recientes (lectura directa)" }

func (recentCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("recent", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	project := fs.String("project", "", "filtrar por proyecto (por defecto KANDEL_PROJECT)")
	scope := fs.String("scope", "", "filtrar por alcance (project|personal|global)")
	limit := fs.Int("limit", 20, "número máximo de memorias")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel recent [--project P] [--scope S] [--limit N]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	var parsedScope domain.Scope
	if *scope != "" {
		s, err := domain.ParseScope(*scope)
		if err != nil {
			return err
		}
		parsedScope = s
	}

	memories, err := app.Memory.Recent(ctx, ports.RecentQuery{
		Project: resolveProject(*project),
		Scope:   parsedScope,
		Limit:   *limit,
	})
	if err != nil {
		return err
	}
	if len(memories) == 0 {
		fmt.Fprintln(app.Out, "sin memorias")
		return nil
	}
	for _, m := range memories {
		fmt.Fprintf(app.Out, "#%d  [%s]  %s\n", m.ID, m.Type, m.Title)
	}
	return nil
}
