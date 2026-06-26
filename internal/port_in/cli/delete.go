package cli

import (
	"context"
	"fmt"
	"strconv"
)

// deleteCmd borra una memoria. Por defecto es soft-delete (reversible); --hard la elimina
// junto con su vector.
type deleteCmd struct{}

func init() { register(deleteCmd{}) }

func (deleteCmd) Name() string     { return "delete" }
func (deleteCmd) Synopsis() string { return "borra una memoria (soft por defecto, --hard total)" }

// Run escanea los argumentos a mano (en vez de usar flag) para que --hard funcione en
// cualquier posición: el paquete flag deja de parsear tras el primer posicional, y un flag
// destructivo silenciosamente ignorado sería peligroso. Acepta tanto "delete 42 --hard" como
// "delete --hard 42".
func (deleteCmd) Run(ctx context.Context, args []string, app *App) error {
	hard := false
	idArg := ""
	for _, a := range args {
		switch a {
		case "--hard", "-hard":
			hard = true
		default:
			if idArg == "" {
				idArg = a
			} else {
				return fmt.Errorf("delete: argumento inesperado %q", a)
			}
		}
	}
	if idArg == "" {
		fmt.Fprintln(app.Err, "uso: kandel delete <id> [--hard]")
		return fmt.Errorf("delete: falta el id")
	}
	id, err := strconv.ParseInt(idArg, 10, 64)
	if err != nil {
		return fmt.Errorf("delete: id inválido %q", idArg)
	}

	if err := app.Memory.Delete(ctx, id, hard); err != nil {
		return err
	}
	kind := "soft-delete"
	if hard {
		kind = "hard-delete"
	}
	fmt.Fprintf(app.Out, "%s memoria #%d\n", kind, id)
	return nil
}
