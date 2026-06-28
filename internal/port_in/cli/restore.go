package cli

import (
	"context"
	"fmt"
	"strconv"
)

// restoreCmd restaura una memoria borrada lógicamente (deshace un soft-delete). El borrado físico
// (--hard) no se puede deshacer: para esos casos no hay restauración posible.
type restoreCmd struct{}

func init() { register(restoreCmd{}) }

func (restoreCmd) Name() string     { return "restore" }
func (restoreCmd) Synopsis() string { return "restaura una memoria borrada lógicamente por id" }

func (restoreCmd) Run(ctx context.Context, args []string, app *App) error {
	if len(args) == 0 {
		fmt.Fprintln(app.Err, "uso: kandel restore <id>")
		return fmt.Errorf("restore: falta el id")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("restore: id inválido %q", args[0])
	}
	m, err := app.Memory.Restore(ctx, id)
	if err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "restaurada memoria #%d: %s\n", m.ID, m.Title)
	return nil
}
