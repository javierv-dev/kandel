package cli

import (
	"context"
	"flag"
	"fmt"
	"text/tabwriter"

	"github.com/javierv-dev/kandel/internal/config"
)

// configCmd muestra o cambia la configuración de kandel (~/.kandel/config). Es la superficie única de
// ajustes (§22): cada opción se resuelve por env > archivo > default. `list` muestra el valor efectivo
// y su origen para que no confunda un env que pisa el archivo.
type configCmd struct{}

func init() { register(configCmd{}) }

func (configCmd) Name() string { return "config" }
func (configCmd) Synopsis() string {
	return "muestra o cambia la configuración (list|get|set|path|gen-token)"
}

func (configCmd) Run(_ context.Context, args []string, app *App) error {
	if len(args) == 0 {
		return configList(app)
	}
	switch args[0] {
	case "list":
		return configList(app)
	case "path":
		dir, err := config.DefaultDir()
		if err != nil {
			return err
		}
		fmt.Fprintln(app.Out, config.FilePath(dir))
		return nil
	case "get":
		if len(args) < 2 {
			return fmt.Errorf("uso: kandel config get <clave>")
		}
		k, ok := config.KeyByFile(args[1])
		if !ok {
			return fmt.Errorf("clave desconocida: %s (usa `kandel config list` para verlas)", args[1])
		}
		fmt.Fprintln(app.Out, config.Resolve(k))
		return nil
	case "set":
		if len(args) < 3 {
			return fmt.Errorf("uso: kandel config set <clave> <valor>")
		}
		k, ok := config.KeyByFile(args[1])
		if !ok {
			return fmt.Errorf("clave desconocida: %s (usa `kandel config list` para verlas)", args[1])
		}
		if k.File == config.SyncURL.File && args[2] != "" {
			if err := config.ValidateSyncURL(args[2]); err != nil {
				return err
			}
		}
		dir, err := config.DefaultDir()
		if err != nil {
			return err
		}
		if err := config.Save(dir, map[string]string{k.File: args[2]}); err != nil {
			return err
		}
		if k.File == config.AgentCLI.File || k.File == config.JudgeModel.File {
			_ = config.ClearJudgeAlert(dir)
		}
		fmt.Fprintf(app.Out, "%s = %s\n", k.File, args[2])
		return nil
	case "gen-token":
		return configGenToken(args[1:], app)
	default:
		return fmt.Errorf("subcomando desconocido: %s (usa list|get|set|path|gen-token)", args[0])
	}
}

// configGenToken genera un token de sync de alta entropía. Sin flags lo imprime (para copiarlo); con
// --set lo guarda en sync_token del archivo de configuración. El token es a la vez namespace y secreto
// del hub: la misma cadena debe ir en todas las máquinas del usuario para compartir sus memorias.
func configGenToken(args []string, app *App) error {
	fs := flag.NewFlagSet("config gen-token", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	set := fs.Bool("set", false, "guarda el token generado en sync_token (~/.kandel/config)")
	fs.Usage = func() {
		fmt.Fprintln(app.Err, "uso: kandel config gen-token [--set]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	token, err := config.GenerateToken()
	if err != nil {
		return err
	}
	if !*set {
		fmt.Fprintln(app.Out, token)
		return nil
	}

	dir, err := config.DefaultDir()
	if err != nil {
		return err
	}
	if err := config.Save(dir, map[string]string{config.SyncToken.File: token}); err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "%s = %s\n", config.SyncToken.File, token)
	fmt.Fprintln(app.Err, "token guardado. Usa el MISMO token en tus otras máquinas para compartir memorias.")
	return nil
}

// configList imprime cada clave conocida con su valor efectivo y su origen (entorno/archivo/default),
// en columnas alineadas.
func configList(app *App) error {
	w := tabwriter.NewWriter(app.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CLAVE\tVALOR\tORIGEN")
	for _, k := range config.Keys {
		value, source := config.ResolveWithSource(k)
		if value == "" {
			value = "(vacío)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", k.File, value, source)
	}
	return w.Flush()
}
