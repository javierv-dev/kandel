package cli

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
)

// backupCmd crea una copia de seguridad en caliente de la base de datos local.
type backupCmd struct{}

func init() { register(backupCmd{}) }

func (backupCmd) Name() string     { return "backup" }
func (backupCmd) Synopsis() string { return "crea una copia de seguridad de la base de datos local" }

func (backupCmd) Run(ctx context.Context, args []string, app *App) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel backup <archivo-destino>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	positionals := fs.Args()
	if len(positionals) != 1 {
		fs.Usage()
		return fmt.Errorf("backup: se requiere exactamente 1 argumento posicional (<archivo-destino>)")
	}

	destPath, err := filepath.Abs(positionals[0])
	if err != nil {
		return fmt.Errorf("backup: ruta inválida: %w", err)
	}

	if err := app.Maintenance.Backup(ctx, destPath); err != nil {
		return err
	}

	fmt.Fprintf(app.Out, "Copia de seguridad completada con éxito en %q\n", destPath)
	return nil
}
