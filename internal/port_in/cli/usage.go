package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"
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
	dormant := fs.Bool("dormant", false, "listar solo las inyectadas repetidamente y nunca mencionadas (desde su línea base)")
	total := fs.Bool("total", false, "mostrar el acumulado de por vida en vez de lo ocurrido desde la línea base")
	rebase := fs.Bool("rebase", false, "fijar la línea base de los ids dados (o de todas las dormidas con --dormant)")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel usage [--limit 20] [--dormant] [--total]")
		fmt.Fprintln(app.Err, "     kandel usage --rebase <id>...   |   kandel usage --rebase --dormant")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if app.Usage == nil {
		return errors.New("usage: la telemetría de uso no está cableada en esta build")
	}

	if *rebase {
		return runUsageRebase(ctx, app, fs.Args(), *dormant)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: argumentos inesperados %q (los ids solo se usan con --rebase)", fs.Args())
	}

	stats, err := app.Usage.Stats(ctx, *limit, *total)
	if err != nil {
		return err
	}
	if len(stats) == 0 {
		fmt.Fprintln(app.Out, "Todavía no hay inyecciones registradas. Los contadores se llenan solos según se use la memoria.")
		return nil
	}

	if *total {
		fmt.Fprintln(app.Out, "Acumulado de por vida (la columna DESDE indica el último corte de línea base, si lo hay).")
	} else {
		fmt.Fprintln(app.Out, "Desde la línea base de cada memoria (última edición o --rebase); --total muestra el acumulado.")
	}
	fmt.Fprintf(app.Out, "%-6s  %-10s  %-10s  %-10s  %s\n", "ID", "INYECTADA", "MENCIONADA", "DESDE", "TÍTULO")
	shown := 0
	for _, s := range stats {
		inj, men := s.Usage.InjectedSince(), s.Usage.MentionedSince()
		if *total {
			inj, men = s.Usage.InjectedCount, s.Usage.MentionedCount
		}
		if *dormant && s.Usage.MentionedSince() > 0 {
			continue
		}
		fmt.Fprintf(app.Out, "%-6d  %-10d  %-10d  %-10s  %s\n",
			s.Memory.ID, inj, men, baselineLabel(s.Usage.BaselinedAt), truncateTitle(s.Memory.Title, 60))
		shown++
	}
	if shown == 0 {
		fmt.Fprintln(app.Out, "(ninguna memoria cumple el filtro)")
		return nil
	}

	n, err := app.Usage.CountDormant(ctx)
	if err == nil && n > 0 {
		fmt.Fprintf(app.Out, "\n%d memoria(s) se inyectan repetidamente sin que el agente las mencione nunca (desde su línea base).\n", n)
		fmt.Fprintln(app.Out, "Reescríbelas para que digan CUÁNDO hacen falta, o bórralas: hoy solo cuestan tokens.")
		fmt.Fprintln(app.Out, "Editar una memoria reinicia su medición sola; tras revisarlas a mano usa `kandel usage --rebase --dormant`.")
	}
	return nil
}

// runUsageRebase fija la línea base de los ids dados, o la de todas las dormidas con --dormant.
// Exige uno de los dos: un --rebase sin objetivo es casi seguro un error de tipeo y no debe
// reiniciar nada por defecto.
func runUsageRebase(ctx context.Context, app *App, rawIDs []string, dormant bool) error {
	if dormant {
		if len(rawIDs) > 0 {
			return errors.New("usage: --rebase acepta ids o --dormant, no ambos")
		}
		n, err := app.Usage.RebaseDormant(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(app.Out, "Línea base fijada en %d memoria(s) dormida(s). Los totales no se tocaron.\n", n)
		return nil
	}
	if len(rawIDs) == 0 {
		return errors.New("usage: --rebase necesita ids (`kandel usage --rebase 213 424`) o --dormant")
	}
	ids := make([]int64, 0, len(rawIDs))
	for _, raw := range rawIDs {
		id, err := strconv.ParseInt(strings.TrimPrefix(raw, "#"), 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("usage: %q no es un id de memoria válido", raw)
		}
		ids = append(ids, id)
	}
	if err := app.Usage.Rebase(ctx, ids); err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "Línea base fijada en %d memoria(s). Los totales no se tocaron.\n", len(ids))
	return nil
}

// baselineLabel formatea la fecha de la línea base para la columna DESDE; sin ella, el origen.
func baselineLabel(t *time.Time) string {
	if t == nil {
		return "origen"
	}
	return t.Local().Format("2006-01-02")
}

// truncateTitle acorta un título largo para que la tabla no se desborde, marcando el corte.
func truncateTitle(title string, max int) string {
	title = strings.TrimSpace(title)
	if len([]rune(title)) <= max {
		return title
	}
	return string([]rune(title)[:max-1]) + "…"
}
