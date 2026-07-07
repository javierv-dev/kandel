package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/javierv-dev/kandel/internal/config"
)

// syncCmd sincroniza la memoria contra el hub (modelo change-feed). Sin flags hace push + pull: sube
// lo local pendiente y baja lo ajeno en un solo comando. Con --push o --pull hace solo esa dirección.
type syncCmd struct{}

func init() { register(syncCmd{}) }

func (syncCmd) Name() string     { return "sync" }
func (syncCmd) Synopsis() string { return "sincroniza la memoria con el hub (push + pull)" }

func (syncCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	onlyPush := fs.Bool("push", false, "solo subir lo local (no bajar)")
	onlyPull := fs.Bool("pull", false, "solo bajar lo ajeno (no subir)")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel sync [--push | --pull]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := requireSyncConfig(); err != nil {
		return err
	}

	// Sin flags (o con ambos) hace las dos direcciones; un solo flag limita a esa dirección.
	both := *onlyPush == *onlyPull
	doPush := both || *onlyPush
	doPull := both || *onlyPull

	var pushed, pulled int
	if doPush {
		res, err := app.Sync.Push(ctx)
		if err != nil {
			return err
		}
		pushed = res.Count
	}
	if doPull {
		res, err := app.Sync.Pull(ctx)
		if err != nil {
			return err
		}
		pulled = res.Applied
	}

	switch {
	case doPush && doPull:
		fmt.Fprintf(app.Out, "sincronizado: %d subida(s), %d bajada(s)\n", pushed, pulled)
	case doPush:
		fmt.Fprintf(app.Out, "subidas %d fila(s)\n", pushed)
	default:
		fmt.Fprintf(app.Out, "bajadas %d fila(s)\n", pulled)
	}
	return nil
}

// requireSyncConfig valida que el sync esté configurado (URL del hub + token). Sin ellos, el sync no
// tiene a dónde ir: falla con un mensaje que dice exactamente qué definir.
func requireSyncConfig() error {
	if strings.TrimSpace(config.Resolve(config.SyncURL)) == "" || strings.TrimSpace(config.Resolve(config.SyncToken)) == "" {
		return errors.New("sync no configurado: define KANDEL_SYNC_URL y KANDEL_SYNC_TOKEN " +
			"(o `kandel config set sync_url <url>` y `kandel config set sync_token <token>`)")
	}
	return nil
}
