package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/javierv-dev/kandel/internal/config"
	"github.com/javierv-dev/kandel/internal/domain"
)

// syncCmd sincroniza la memoria contra el hub (modelo change-feed). Sin flags hace push + pull; con
// --push o --pull hace solo esa dirección. El subcomando `conflicts` gestiona las ediciones
// concurrentes detectadas (§6.5): listarlas y resolverlas.
type syncCmd struct{}

func init() { register(syncCmd{}) }

func (syncCmd) Name() string     { return "sync" }
func (syncCmd) Synopsis() string { return "sincroniza la memoria con el hub (push + pull)" }

func (syncCmd) Run(ctx context.Context, args []string, app *App) error {
	// Subcomando de conflictos (no toca el hub: es todo local).
	if len(args) > 0 && args[0] == "conflicts" {
		return runConflicts(ctx, args[1:], app)
	}

	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	onlyPush := fs.Bool("push", false, "solo subir lo local (no bajar)")
	onlyPull := fs.Bool("pull", false, "solo bajar lo ajeno (no subir)")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel sync [--push | --pull]")
		fmt.Fprintln(app.Err, "     kandel sync conflicts [--take <sync_id> | --keep <sync_id>]")
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

	var pushed, pulled, conflicts, schemaDrift int
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
		conflicts = res.Conflicts
		schemaDrift = res.SchemaDrift
	}

	switch {
	case doPush && doPull:
		fmt.Fprintf(app.Out, "sincronizado: %d subida(s), %d bajada(s)\n", pushed, pulled)
	case doPush:
		fmt.Fprintf(app.Out, "subidas %d fila(s)\n", pushed)
	default:
		fmt.Fprintf(app.Out, "bajadas %d fila(s)\n", pulled)
	}
	if conflicts > 0 {
		fmt.Fprintf(app.Out, "⚠️ %d edición(es) concurrente(s): se editó aquí y en otra máquina a la vez (NO se pisó nada). "+
			"Revisa con `kandel sync conflicts`\n", conflicts)
	}
	if schemaDrift > 0 {
		fmt.Fprintf(app.Out, "⚠️ %d memoria(s) traen campos de una versión más nueva de kandel (se preservan intactos; considera actualizar este binario)\n", schemaDrift)
	}
	return nil
}

// runConflicts lista o resuelve los conflictos de edición concurrente. Sin flags lista; --take aplica
// la versión remota de esa memoria; --keep conserva la local (descarta la remota de la bandeja).
func runConflicts(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("sync conflicts", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	take := fs.String("take", "", "resuelve tomando la versión remota de esa memoria (sync_id)")
	keep := fs.String("keep", "", "resuelve conservando la versión local de esa memoria (sync_id)")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel sync conflicts [--take <sync_id> | --keep <sync_id>]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *take != "" && *keep != "" {
		return errors.New("usa solo uno: --take o --keep")
	}
	if *take != "" {
		if err := app.Sync.ResolveConflict(ctx, *take, true); err != nil {
			return err
		}
		fmt.Fprintf(app.Out, "conflicto de %s resuelto: se tomó la versión remota\n", *take)
		return nil
	}
	if *keep != "" {
		if err := app.Sync.ResolveConflict(ctx, *keep, false); err != nil {
			return err
		}
		fmt.Fprintf(app.Out, "conflicto de %s resuelto: se conservó la versión local\n", *keep)
		return nil
	}

	// Sin flags: listar.
	list, err := app.Sync.ListConflicts(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(app.Out, "no hay ediciones concurrentes pendientes")
		return nil
	}
	fmt.Fprintf(app.Out, "%d edición(es) concurrente(s) pendiente(s):\n", len(list))
	for _, c := range list {
		fmt.Fprintf(app.Out, "- %s%s (remota: %s)\n", c.SyncID, projectSuffix(c.Project), incomingTitle(c))
	}
	fmt.Fprintln(app.Out, "resuelve con: kandel sync conflicts --take <sync_id> | --keep <sync_id>")
	return nil
}

// incomingTitle intenta leer el título de la memoria remota del payload para mostrarlo; si no puede,
// describe la operación.
func incomingTitle(c domain.SyncConflict) string {
	if c.IncomingOp == domain.SyncOpDelete {
		return "borrada en la otra máquina"
	}
	var m domain.Memory
	if err := json.Unmarshal(c.Incoming, &m); err != nil || strings.TrimSpace(m.Title) == "" {
		return "cambio remoto"
	}
	return m.Title
}

// projectSuffix devuelve " [proyecto]" si hay proyecto, o vacío.
func projectSuffix(project string) string {
	if strings.TrimSpace(project) == "" {
		return ""
	}
	return " [" + project + "]"
}

// requireSyncConfig valida que el sync esté configurado (URL del hub + token) y que la URL use un
// transporte seguro (https:// en remoto, http:// solo en loopback).
func requireSyncConfig() error {
	rawURL := strings.TrimSpace(config.Resolve(config.SyncURL))
	if rawURL == "" || strings.TrimSpace(config.Resolve(config.SyncToken)) == "" {
		return errors.New("sync no configurado: define KANDEL_SYNC_URL y KANDEL_SYNC_TOKEN " +
			"(o `kandel config set sync_url <url>` y `kandel config set sync_token <token>`)")
	}
	return config.ValidateSyncURL(rawURL)
}
