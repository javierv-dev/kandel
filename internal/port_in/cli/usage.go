package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
)

// usageCmd lista qué memorias se están inyectando de verdad y cuáles de ellas el agente acaba
// usando. Es la vista que hace posible podar con datos: sin ella se guarda mucho, no se sabe qué
// sirve, y cualquier ajuste de umbrales o de contenido es a ciegas.
type usageCmd struct{}

func init() { register(usageCmd{}) }

func (usageCmd) Name() string { return "usage" }
func (usageCmd) Synopsis() string {
	return "telemetría de uso de la memoria (inyecciones y menciones)"
}

func (usageCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	limit := fs.Int("limit", 20, "cuántas memorias listar")
	dormant := fs.Bool("dormant", false, "listar solo las inyectadas repetidamente y nunca mencionadas")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel usage [--limit 20] [--dormant]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if app.Usage == nil {
		return errors.New("usage: la telemetría de uso no está cableada en esta build")
	}

	stats, err := app.Usage.Stats(ctx, *limit)
	if err != nil {
		return err
	}
	if len(stats) == 0 {
		fmt.Fprintln(app.Out, "Todavía no hay inyecciones registradas. Los contadores se llenan solos según se use la memoria.")
		return nil
	}

	fmt.Fprintf(app.Out, "%-6s  %-10s  %-10s  %s\n", "ID", "INYECTADA", "MENCIONADA", "TÍTULO")
	shown := 0
	for _, s := range stats {
		if *dormant && s.Usage.MentionedCount > 0 {
			continue
		}
		fmt.Fprintf(app.Out, "%-6d  %-10d  %-10d  %s\n",
			s.Memory.ID, s.Usage.InjectedCount, s.Usage.MentionedCount, truncateTitle(s.Memory.Title, 60))
		shown++
	}
	if shown == 0 {
		fmt.Fprintln(app.Out, "(ninguna memoria cumple el filtro)")
		return nil
	}

	n, err := app.Usage.CountDormant(ctx)
	if err == nil && n > 0 {
		fmt.Fprintf(app.Out, "\n%d memoria(s) se inyectan repetidamente sin que el agente las mencione nunca.\n", n)
		fmt.Fprintln(app.Out, "Reescríbelas para que digan CUÁNDO hacen falta, o bórralas: hoy solo cuestan tokens.")
	}
	return nil
}

// truncateTitle acorta un título largo para que la tabla no se desborde, marcando el corte.
func truncateTitle(title string, max int) string {
	title = strings.TrimSpace(title)
	if len([]rune(title)) <= max {
		return title
	}
	return string([]rune(title)[:max-1]) + "…"
}
