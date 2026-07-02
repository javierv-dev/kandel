package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/javierv-dev/kandel/internal/adapter/auditlog"
	"github.com/javierv-dev/kandel/internal/config"
	"github.com/javierv-dev/kandel/internal/port_in/tui"
)

// tuiCmd arranca la interfaz de terminal interactiva para navegar las memorias (lectura directa).
type tuiCmd struct{}

func init() { register(tuiCmd{}) }

func (tuiCmd) Name() string     { return "tui" }
func (tuiCmd) Synopsis() string { return "interfaz de terminal para navegar memorias" }

func (tuiCmd) Run(_ context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	project := fs.String("project", "", "filtrar por proyecto (por defecto KANDEL_PROJECT)")
	limit := fs.Int("limit", 50, "número de memorias a cargar")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel tui [--project P] [--limit N]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	logger := auditlog.New(filepath.Join(tuiLogDir(), "tui-actions.jsonl"))
	return tui.Run(app.Memory, logger, resolveProject(*project), *limit)
}

// tuiLogDir resuelve el directorio de logs (KANDEL_LOG_DIR o ~/.kandel/logs), mismo lugar que el
// resto de la observabilidad de kandel. Si no se puede resolver el home, cae al directorio actual.
func tuiLogDir() string {
	if d := config.Resolve(config.LogDir); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "logs"
	}
	return filepath.Join(home, ".kandel", "logs")
}
