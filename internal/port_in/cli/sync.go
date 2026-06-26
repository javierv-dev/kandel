package cli

import (
	"context"
	"flag"
	"fmt"
)

// syncCmd sincroniza la memoria por chunks en un directorio (.kandel/). Por defecto EXPORTA la
// cola local; con --import APLICA los chunks ajenos del directorio. El movimiento real entre
// máquinas lo hace git (el usuario commitea/pushea .kandel/).
type syncCmd struct{}

func init() { register(syncCmd{}) }

func (syncCmd) Name() string     { return "sync" }
func (syncCmd) Synopsis() string { return "exporta/importa memoria por chunks (.kandel/)" }

func (syncCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	doImport := fs.Bool("import", false, "importar chunks ajenos en vez de exportar")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel sync [--import]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *doImport {
		res, err := app.Sync.Import(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(app.Out, "importados %d chunk(s), %d mutación(es) aplicada(s)\n", res.ChunksImported, res.MutationsApplied)
		return nil
	}

	res, err := app.Sync.Export(ctx)
	if err != nil {
		return err
	}
	if res.Count == 0 {
		fmt.Fprintln(app.Out, "nada que exportar (cola vacía)")
		return nil
	}
	fmt.Fprintf(app.Out, "exportado chunk %s con %d mutación(es)\n", res.ChunkID, res.Count)
	fmt.Fprintln(app.Out, "comparte con: git add .kandel/ && git commit -m \"sync kandel\"")
	return nil
}
