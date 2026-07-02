package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/javierv-dev/kandel/internal/adapter/llm"
	"github.com/javierv-dev/kandel/internal/config"
)

// agentCmd gestiona el agente activo que juzga conflictos y siembra (§22.D/F). `list` muestra los
// agentes instalados y cuál está activo; `use` cambia el activo (p. ej. si al agente actual se le
// acaba el plan) validando que esté instalado y soportado. Es azúcar sobre `config set agent_cli`.
type agentCmd struct{}

func init() { register(agentCmd{}) }

func (agentCmd) Name() string     { return "agent" }
func (agentCmd) Synopsis() string { return "gestiona el agente activo del juez/seed (list|use)" }

func (agentCmd) Run(_ context.Context, args []string, app *App) error {
	dir, err := config.DefaultDir()
	if err != nil {
		return err
	}
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "list":
		return agentList(app, dir)
	case "use":
		if len(args) < 2 {
			return fmt.Errorf("uso: kandel agent use <nombre>")
		}
		return agentUse(app, dir, args[1])
	default:
		return fmt.Errorf("subcomando desconocido: %s (usa list|use)", sub)
	}
}

// agentList muestra los agentes instalados marcando el activo.
func agentList(app *App, dir string) error {
	active := strings.TrimSpace(config.Get(dir, config.AgentCLI.File))
	installed := config.Agents(dir)
	if len(installed) == 0 {
		fmt.Fprintln(app.Out, "no hay agentes instalados (instala uno con `kandel install <agente>`)")
		return nil
	}
	for _, a := range installed {
		marker := "  "
		if a == active {
			marker = "* "
		}
		fmt.Fprintf(app.Out, "%s%s\n", marker, a)
	}
	return nil
}

// agentUse cambia el agente activo. Valida que el nombre esté instalado y que kandel implemente su
// contrato de CLI (allowlist de llm); si no, falla con un aviso claro en vez de dejar un juez inútil.
func agentUse(app *App, dir, name string) error {
	name = strings.TrimSpace(name)
	if !containsString(config.Agents(dir), name) {
		return fmt.Errorf("el agente %q no está instalado (instalados: %s)", name,
			strings.Join(config.Agents(dir), ", "))
	}
	if !llm.IsSupported(name) {
		return fmt.Errorf("el agente %q no está soportado todavía (soportados: %s)", name,
			strings.Join(llm.SupportedAgents(), ", "))
	}
	if err := config.Save(dir, map[string]string{config.AgentCLI.File: name}); err != nil {
		return err
	}
	fmt.Fprintf(app.Out, "agente activo: %s\n", name)
	return nil
}

// containsString indica si s está en la lista.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
