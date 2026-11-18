package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

type handoffCmd struct{}

func init()                         { register(handoffCmd{}) }
func (handoffCmd) Name() string     { return "handoff" }
func (handoffCmd) Synopsis() string { return "consulta relevos en la copia de trabajo local" }
func (handoffCmd) Run(ctx context.Context, args []string, app *App) error {
	if app.Handoff == nil {
		return fmt.Errorf("relevo no disponible")
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(app.Out)
	enc.SetIndent("", "  ")
	switch {
	case len(args) == 1 && args[0] == "list":
		hs, err := app.Handoff.List(ctx, dir)
		if err != nil {
			return err
		}
		return enc.Encode(hs)
	case len(args) == 2 && args[0] == "show":
		h, err := app.Handoff.Get(ctx, dir, args[1])
		if err != nil {
			return err
		}
		return enc.Encode(h)
	case len(args) == 3 && args[0] == "resume":
		// Comprueba tanto la copia de trabajo del operador como la de la sesión declarada.
		if _, err := app.Handoff.Get(ctx, dir, args[1]); err != nil {
			return err
		}
		h, err := app.Handoff.Resume(ctx, args[2], args[1])
		if err != nil {
			return err
		}
		return enc.Encode(h)
	default:
		return fmt.Errorf("uso: kandel handoff list | show <checkpoint_id> | resume <checkpoint_id> <session_id>")
	}
}
