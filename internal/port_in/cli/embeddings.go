package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// embeddingsCmd agrupa el mantenimiento de los vectores: backfill (rellenar los que faltan o
// quedaron obsoletos) y reembed (forzar el recálculo de uno). Necesita Ollama corriendo, a
// diferencia de las lecturas directas del usuario.
type embeddingsCmd struct{}

func init() { register(embeddingsCmd{}) }

func (embeddingsCmd) Name() string     { return "embeddings" }
func (embeddingsCmd) Synopsis() string { return "mantenimiento de vectores (backfill|reembed)" }

func (embeddingsCmd) Run(ctx context.Context, args []string, app *App) error {
	if len(args) == 0 {
		fmt.Fprintln(app.Err, "uso: kandel embeddings <backfill|reembed> [argumentos]")
		return fmt.Errorf("embeddings: falta el subcomando")
	}
	switch args[0] {
	case "backfill":
		return embeddingsBackfill(ctx, app)
	case "reembed":
		return embeddingsReembed(ctx, args[1:], app)
	default:
		fmt.Fprintln(app.Err, "uso: kandel embeddings <backfill|reembed> [argumentos]")
		return fmt.Errorf("embeddings: subcomando desconocido %q", args[0])
	}
}

// embeddingsBackfill calcula el vector de toda memoria que no lo tenga o lo tenga obsoleto.
func embeddingsBackfill(ctx context.Context, app *App) error {
	res, err := app.Embedding.Backfill(ctx)
	if err != nil {
		// El error puede ser parcial: se informa cuántas se procesaron antes de fallar.
		fmt.Fprintf(app.Err, "backfill interrumpido tras %d memoria(s); reejecuta para continuar\n", res.Embedded)
		return err
	}
	fmt.Fprintf(app.Out, "backfill: %d memoria(s) con vector al día\n", res.Embedded)
	if res.Skipped > 0 {
		fmt.Fprintf(app.Err, "backfill: %d memoria(s) saltada(s) porque el modelo de embeddings no pudo vectorizar su texto (ids: %s)\n",
			res.Skipped, joinIDs(res.SkippedIDs))
	}
	return nil
}

// joinIDs formatea una lista de ids como "1, 2, 3" para los mensajes al usuario.
func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ", ")
}

// embeddingsReembed fuerza el recálculo del vector de una memoria por id.
func embeddingsReembed(ctx context.Context, args []string, app *App) error {
	if len(args) < 1 {
		fmt.Fprintln(app.Err, "uso: kandel embeddings reembed <id>")
		return fmt.Errorf("reembed: falta el id")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("reembed: id inválido %q", args[0])
	}
	if err := app.Embedding.Reembed(ctx, id); err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "reembed: vector recalculado para la memoria #%d\n", id)
	return nil
}
