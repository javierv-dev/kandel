package cli

import (
	"context"
	"fmt"
	"strconv"
)

// deleteCmd borra una memoria de forma LÓGICA (soft-delete, reversible con restore). No existe
// borrado físico: toda eliminación es reversible.
type deleteCmd struct{}

func init() { register(deleteCmd{}) }

func (deleteCmd) Name() string     { return "delete" }
func (deleteCmd) Synopsis() string { return "borra una memoria (lógico, reversible con restore)" }

func (deleteCmd) Run(ctx context.Context, args []string, app *App) error {
	idArg := ""
	for _, a := range args {
		if idArg == "" {
			idArg = a
		} else {
			return fmt.Errorf("delete: argumento inesperado %q", a)
		}
	}
	if idArg == "" {
		fmt.Fprintln(app.Err, "uso: kandel delete <id>")
		return fmt.Errorf("delete: falta el id")
	}
	id, err := strconv.ParseInt(idArg, 10, 64)
	if err != nil {
		return fmt.Errorf("delete: id inválido %q", idArg)
	}

	if err := app.Memory.Delete(ctx, id); err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "soft-delete memoria #%d\n", id)
	return nil
}
