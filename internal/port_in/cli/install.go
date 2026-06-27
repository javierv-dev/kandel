package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/javierv-dev/kandel/internal/setup"
)

func init() {
	register(installCmd{})
	register(uninstallCmd{})
}

// installCmd cablea kandel en la configuración de un agente (skill + MCP + hooks). No es un plugin:
// escribe la config del usuario del agente (ver §18 del plan).
type installCmd struct{}

func (installCmd) Name() string     { return "install" }
func (installCmd) Synopsis() string { return "instala la integración de kandel en un agente" }

func (installCmd) Run(_ context.Context, args []string, app *App) error {
	name, err := agentArg(args, app)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("no se pudo resolver el directorio del usuario: %w", err)
	}
	return setup.Install(name, home, app.Out)
}

// uninstallCmd retira la integración de kandel de un agente, sin pisar su configuración propia.
type uninstallCmd struct{}

func (uninstallCmd) Name() string     { return "uninstall" }
func (uninstallCmd) Synopsis() string { return "retira la integración de kandel de un agente" }

func (uninstallCmd) Run(_ context.Context, args []string, app *App) error {
	name, err := agentArg(args, app)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("no se pudo resolver el directorio del usuario: %w", err)
	}
	return setup.Uninstall(name, home, app.Out)
}

// agentArg extrae el nombre del agente del primer posicional; si falta, lista los soportados y
// devuelve error.
func agentArg(args []string, app *App) (string, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintf(app.Err, "falta el agente. Soportados: %s\n", strings.Join(setup.Names(), ", "))
		return "", fmt.Errorf("falta el nombre del agente")
	}
	return strings.TrimSpace(args[0]), nil
}
