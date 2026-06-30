package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// searchCmd es la búsqueda semántica del agente: consulta en texto → vector → vecinos por coseno.
type searchCmd struct{}

func init() { register(searchCmd{}) }

func (searchCmd) Name() string     { return "search" }
func (searchCmd) Synopsis() string { return "busca memorias por significado (semántica)" }

func (searchCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	typ := fs.String("type", "", "filtrar por tipo")
	project := fs.String("project", "", "filtrar por proyecto (por defecto KANDEL_PROJECT)")
	scope := fs.String("scope", "", "filtrar por alcance (project|global)")
	limit := fs.Int("limit", 10, "número máximo de resultados")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel search <consulta> [--type T] [--project P] [--scope S] [--limit N]")
		fs.PrintDefaults()
	}
	positionals, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positionals) < 1 {
		fs.Usage()
		return fmt.Errorf("search: falta la consulta")
	}

	var parsedScope domain.Scope
	if *scope != "" {
		s, err := domain.ParseScope(*scope)
		if err != nil {
			return err
		}
		parsedScope = s
	}

	results, err := app.Memory.Search(ctx, ports.SearchQuery{
		Query:   positionals[0],
		Type:    domain.MemoryType(*typ),
		Project: resolveProject(*project),
		Scope:   parsedScope,
		Limit:   *limit,
	})
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Fprintln(app.Out, "sin resultados")
		return nil
	}
	for _, r := range results {
		fmt.Fprintf(app.Out, "#%d  [%.3f]  %s\n", r.Memory.ID, r.Score, r.Memory.Title)
	}
	return nil
}
