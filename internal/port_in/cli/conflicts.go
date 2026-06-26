package cli

import (
	"context"
	"flag"
	"fmt"
	"strconv"
)

// conflictsCmd lista las memorias semánticamente más cercanas a una dada (posibles conflictos).
// Es read-only y barato: no invoca al juez LLM. El juicio (mem_compare/mem_judge) se hace por MCP.
type conflictsCmd struct{}

func init() { register(conflictsCmd{}) }

func (conflictsCmd) Name() string     { return "conflicts" }
func (conflictsCmd) Synopsis() string { return "lista posibles conflictos de una memoria (semántico)" }

func (conflictsCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("conflicts", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	limit := fs.Int("limit", 5, "número máximo de candidatos")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel conflicts <id> [--limit N]")
		fs.PrintDefaults()
	}
	positionals, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positionals) < 1 {
		fs.Usage()
		return fmt.Errorf("conflicts: falta el id de la memoria")
	}
	id, err := strconv.ParseInt(positionals[0], 10, 64)
	if err != nil {
		return fmt.Errorf("conflicts: id inválido %q", positionals[0])
	}

	candidates, err := app.Conflict.FindCandidates(ctx, id, *limit)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		fmt.Fprintln(app.Out, "sin candidatos a conflicto")
		return nil
	}
	for _, c := range candidates {
		fmt.Fprintf(app.Out, "#%d  [%.3f]  %s\n", c.Memory.ID, c.Score, c.Memory.Title)
	}
	return nil
}
