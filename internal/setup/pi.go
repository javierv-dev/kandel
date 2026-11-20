package setup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/javierv-dev/kandel/internal/adapter/llm"
	"github.com/javierv-dev/kandel/internal/config"
)

func init() { register(piAgent{}) }

// piAgent cablea kandel en Pi Agent: escribe las skills en ~/.pi/agent/skills/, registra el servidor
// MCP en ~/.pi/agent/mcp.json (con exposure="deferred" y toolExposure="direct" para las tools de uso
// continuo) e instala la extensión de ciclo de vida en ~/.pi/agent/extensions/kandel.ts.
// También registra "pi" en installed_agents (y como agent_cli activo si es el primero en instalarse,
// regla first-install-wins §22.D).
type piAgent struct{}

func (piAgent) Name() string { return "pi" }

// piAgentCLI es el nombre de la CLI de Pi Agent (para el juez y el seed).
const piAgentCLI = "pi"

// paths devuelve las rutas de los artefactos de Pi Agent. Respeta PI_CODING_AGENT_DIR si está
// definida en el entorno y apunta dentro del home o cuando se sobreescribe explícitamente; por
// defecto usa ~/.pi/agent.
func (piAgent) paths(home string) (piDir, skillFile, seedSkillFile, mcpFile, extFile string) {
	piDir = filepath.Join(home, ".pi", "agent")
	if envDir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); envDir != "" {
		piDir = envDir
	}
	skillFile = filepath.Join(piDir, "skills", "kandel", "SKILL.md")
	seedSkillFile = filepath.Join(piDir, "skills", "kandel-seed", "SKILL.md")
	mcpFile = filepath.Join(piDir, "mcp.json")
	extFile = filepath.Join(piDir, "extensions", "kandel.ts")
	return
}

// versionFile es la ruta del marcador de versión de los assets instalados (§21 Eje 3). Vive bajo
// ~/.pi/agent/kandel/VERSION, así el uninstall (que borra esa carpeta) lo retira solo.
func (p piAgent) versionFile(home string) string {
	piDir, _, _, _, _ := p.paths(home)
	return filepath.Join(piDir, "kandel", "VERSION")
}

func (p piAgent) Install(home, version string, out io.Writer) error {
	_, skillFile, seedSkillFile, mcpFile, extFile := p.paths(home)

	// 1. Skill principal.
	if err := writeAsset("assets/skill/SKILL.md", skillFile, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ skill escrita en %s\n", skillFile)

	// 1b. Skill del seed (/skill:kandel-seed).
	if err := writeAsset("assets/skill-seed/SKILL.md", seedSkillFile, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ skill de seed escrita en %s\n", seedSkillFile)

	// 2. Extensión de ciclo de vida en ~/.pi/agent/extensions/kandel.ts.
	if err := writeAsset("assets/pi/kandel.ts", extFile, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ extensión de ciclo de vida escrita en %s\n", extFile)

	// 3. Registro del servidor MCP en ~/.pi/agent/mcp.json.
	if err := p.installMCP(mcpFile); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ servidor MCP 'kandel' registrado en %s\n", mcpFile)

	// 4. Config local (~/.kandel/config): agente activo + registro de agentes instalados (§22.D).
	confDir := filepath.Join(home, ".kandel")
	if strings.TrimSpace(config.Get(confDir, config.AgentCLI.File)) == "" {
		if err := config.Save(confDir, map[string]string{
			config.AgentCLI.File:  piAgentCLI,
			config.SeedModel.File: llm.SeedModelFor(piAgentCLI),
		}); err != nil {
			return err
		}
	}
	if err := config.AddAgent(confDir, piAgentCLI); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ config de agente escrita en %s (activo: %s)\n",
		filepath.Join(confDir, "config"), config.Get(confDir, config.AgentCLI.File))

	// 5. Marca de versión de los assets (§21 Eje 3).
	vFile := p.versionFile(home)
	if err := writeTextFile(vFile, version, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ versión de assets (%s) marcada en %s\n", version, vFile)

	fmt.Fprintln(out, "\nListo. Reinicia Pi Agent (o ejecuta /reload) para que tome la configuración.")
	fmt.Fprintln(out, "Las memorias se comparten con los demás agentes de este proyecto (Claude Code, Antigravity).")
	fmt.Fprintln(out, "Requisito: Ollama corriendo con el modelo de embeddings (por defecto qwen3-embedding:0.6b).")
	fmt.Fprintln(out, "  Si falta: ollama pull qwen3-embedding:0.6b")
	return nil
}

func (p piAgent) Uninstall(home string, out io.Writer) error {
	piDir, _, _, mcpFile, extFile := p.paths(home)

	// 1. Quita el servidor MCP.
	if err := p.uninstallMCP(mcpFile); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ servidor MCP 'kandel' retirado de %s\n", mcpFile)

	// 2. Borra la extensión de ciclo de vida de kandel sin tocar otras extensiones.
	if err := os.Remove(extFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("borrar extensión %q: %w", extFile, err)
	}
	fmt.Fprintf(out, "✓ extensión retirada de %s\n", extFile)

	// 3. Borra las skills y la carpeta propia de kandel.
	if err := os.RemoveAll(filepath.Join(piDir, "skills", "kandel")); err != nil {
		return fmt.Errorf("borrar skill: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(piDir, "skills", "kandel-seed")); err != nil {
		return fmt.Errorf("borrar skill de seed: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(piDir, "kandel")); err != nil {
		return fmt.Errorf("borrar carpeta de kandel: %w", err)
	}
	fmt.Fprintln(out, "✓ skills y carpeta de kandel borradas")

	// 4. Quita el agente del registro de instalados y, si era el activo, reasigna al que quede (§22.D).
	confDir := filepath.Join(home, ".kandel")
	if err := config.RemoveAgent(confDir, piAgentCLI); err != nil {
		return err
	}
	if strings.TrimSpace(config.Get(confDir, config.AgentCLI.File)) == piAgentCLI {
		replacement := ""
		if remaining := config.Agents(confDir); len(remaining) > 0 {
			replacement = remaining[0]
		}
		if err := config.Save(confDir, map[string]string{config.AgentCLI.File: replacement}); err != nil {
			return err
		}
		if replacement != "" {
			fmt.Fprintf(out, "✓ agente activo reasignado a %s\n", replacement)
		} else {
			fmt.Fprintln(out, "✓ agente activo desconfigurado (no quedan agentes instalados)")
		}
	}
	return nil
}

// InstalledVersion lee el marcador de versión de los assets. present es false si no existe.
func (p piAgent) InstalledVersion(home string) (string, bool, error) {
	data, err := os.ReadFile(p.versionFile(home))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("leer versión de assets: %w", err)
	}
	return strings.TrimSpace(string(data)), true, nil
}

// installMCP registra el servidor MCP de kandel en mcpServers.kandel de ~/.pi/agent/mcp.json.
// Configura exposure="deferred" con toolExposure="direct" para las herramientas principales
// (mem_save, mem_search, mem_context, mem_get, mem_update, mem_handoff_*), de modo que el modelo en
// Pi pueda invocarlas directamente sin un turno extra de tool_search, dejando las demás en deferred.
func (piAgent) installMCP(mcpFile string) error {
	cfg, err := loadJSONObject(mcpFile)
	if err != nil {
		return err
	}
	servers := mapAt(cfg, "mcpServers")
	servers["kandel"] = map[string]any{
		"command":  "kandel",
		"args":     []any{"mcp", "--tools=agent", "--source=pi"},
		"exposure": "deferred",
		"toolExposure": map[string]any{
			"mem_save":      "direct",
			"mem_search":    "direct",
			"mem_context":   "direct",
			"mem_get":       "direct",
			"mem_update":    "direct",
			"mem_handoff_*": "direct",
		},
	}
	return saveJSONObject(mcpFile, cfg)
}

// uninstallMCP quita la clave mcpServers.kandel sin tocar otros servidores.
func (piAgent) uninstallMCP(mcpFile string) error {
	cfg, err := loadJSONObject(mcpFile)
	if err != nil {
		return err
	}
	servers, ok := cfg["mcpServers"].(map[string]any)
	if !ok {
		return nil
	}
	delete(servers, "kandel")
	if len(servers) == 0 {
		delete(cfg, "mcpServers")
	}
	return saveJSONObject(mcpFile, cfg)
}
