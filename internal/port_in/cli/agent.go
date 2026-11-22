package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/javierv-dev/kandel/internal/adapter/llm"
	"github.com/javierv-dev/kandel/internal/config"
)

// agentCmd gestiona el agente activo que juzga conflictos y siembra (§22.D/F). `list` muestra los
// agentes instalados y cuál está activo; `use` cambia el activo (p. ej. si al agente actual se le
// acaba el plan) validando que esté instalado y soportado, y opcionalmente fija el modelo del juez
// (--model) y/o del seed (--seed-model). Es azúcar sobre `config set agent_cli` + limpieza de alertas.
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
			return fmt.Errorf("uso: kandel agent use <nombre> [--model <modelo-juez>] [--seed-model <modelo-seed>]")
		}
		return agentUse(app, dir, args[1:])
	default:
		return fmt.Errorf("subcomando desconocido: %s (usa list|use)", sub)
	}
}

// agentList muestra los agentes instalados marcando el activo y el modelo efectivo del juez/seed.
func agentList(app *App, dir string) error {
	active := strings.TrimSpace(config.Get(dir, config.AgentCLI.File))
	installed := config.Agents(dir)
	if len(installed) == 0 {
		fmt.Fprintln(app.Out, "no hay agentes instalados (instala uno con `kandel install <agente>`)")
		return nil
	}
	for _, a := range installed {
		marker := "  "
		suffix := ""
		if a == active {
			marker = "* "
			jm := strings.TrimSpace(config.Get(dir, config.JudgeModel.File))
			if jm == "" {
				jm = llm.JudgeModelFor(a)
			}
			sm := strings.TrimSpace(config.Get(dir, config.SeedModel.File))
			if sm == "" {
				sm = llm.SeedModelFor(a)
			}
			if jm != "" || sm != "" {
				suffix = fmt.Sprintf(" (juez: %s, seed: %s)", jm, sm)
			}
		}
		fmt.Fprintf(app.Out, "%s%s%s\n", marker, a, suffix)
	}
	return nil
}

// agentUse cambia el agente activo (y opcionalmente el modelo del juez y del seed). Valida que el
// nombre esté instalado y que kandel implemente su contrato de CLI (allowlist de llm); si no, falla
// con un aviso claro en vez de dejar un juez inútil. Al cambiar el agente/modelo limpia cualquier
// alerta de fallo previa del juez para que el daemon retome la detección de conflictos.
func agentUse(app *App, dir string, args []string) error {
	var name string
	var flagArgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
			if !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		} else if name == "" {
			name = a
		} else {
			flagArgs = append(flagArgs, a)
		}
	}

	fs := flag.NewFlagSet("agent use", flag.ContinueOnError)
	fs.SetOutput(app.Err)
	judgeModel := fs.String("model", "", "modelo específico para el juez de conflictos (vacío = default del agente)")
	seedModel := fs.String("seed-model", "", "modelo específico para el seed (vacío = default del agente)")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if name == "" && fs.NArg() > 0 {
		name = fs.Arg(0)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("uso: kandel agent use <nombre> [--model <modelo-juez>] [--seed-model <modelo-seed>]")
	}

	if !containsString(config.Agents(dir), name) {
		return fmt.Errorf("el agente %q no está instalado (instalados: %s)", name,
			strings.Join(config.Agents(dir), ", "))
	}
	if !llm.IsSupported(name) {
		return fmt.Errorf("el agente %q no está soportado todavía (soportados: %s)", name,
			strings.Join(llm.SupportedAgents(), ", "))
	}
	// Fija el agente activo y alinea los modelos de juez y seed (§22.E): si el usuario no pasó
	// --model, usa el default del agente elegido para que no quede un modelo de otro proveedor.
	jm := strings.TrimSpace(*judgeModel)
	if jm == "" {
		jm = llm.JudgeModelFor(name)
	}
	sm := strings.TrimSpace(*seedModel)
	if sm == "" {
		sm = llm.SeedModelFor(name)
	}
	updates := map[string]string{
		config.AgentCLI.File:   name,
		config.JudgeModel.File: jm,
	}
	if sm != "" {
		updates[config.SeedModel.File] = sm
	}
	if err := config.Save(dir, updates); err != nil {
		return err
	}
	// Limpia cualquier evento/alerta previa de fallo del juez para que el daemon reanude con el nuevo agente/modelo.
	_ = config.ClearJudgeAlert(dir)
	fmt.Fprintf(app.Out, "agente activo: %s (juez: %s, seed: %s)\n", name, jm, sm)
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
