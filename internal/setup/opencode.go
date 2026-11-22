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

func init() { register(opencode{}) }

// opencode cablea kandel en OpenCode v2: registra el servidor MCP en
// ~/.config/opencode/opencode.json (bajo mcp.servers.kandel con type:"local", command en array y
// disabled:false), añade el protocolo de memoria en ~/.config/opencode/AGENTS.md, escribe las
// skills en ~/.config/opencode/skills/ y despliega el plugin V2 en ~/.config/opencode/plugins/kandel.js.
// También gestiona agent_cli/installed_agents en ~/.kandel/config (regla first-install-wins §22.D).
type opencode struct{}

func (opencode) Name() string { return "opencode" }

// opencodeAgentCLI es el nombre de la CLI de agente de OpenCode (para el juez y el seed).
const opencodeAgentCLI = "opencode"

// configDir resuelve el directorio global de OpenCode respetando $XDG_CONFIG_HOME (como hace
// OpenCode v2) con respaldo a ~/.config/opencode.
func (opencode) configDir(home string) string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	return filepath.Join(home, ".config", "opencode")
}

// paths devuelve las rutas de los artefactos de OpenCode v2.
func (o opencode) paths(home string) (ocDir, mcpFile, rulesFile, skillFile, seedSkillFile, pluginFile string) {
	ocDir = o.configDir(home)
	mcpFile = filepath.Join(ocDir, "opencode.json")
	rulesFile = filepath.Join(ocDir, "AGENTS.md")
	// OpenCode exige que el nombre del directorio coincida con el frontmatter `name` de la skill.
	skillFile = filepath.Join(ocDir, "skills", "kandel-memory", "SKILL.md")
	seedSkillFile = filepath.Join(ocDir, "skills", "kandel-seed", "SKILL.md")
	pluginFile = filepath.Join(ocDir, "plugins", "kandel.js")
	return
}

func (opencode) dataDir(home string) string {
	if envDir := strings.TrimSpace(os.Getenv("KANDEL_DATA_DIR")); envDir != "" {
		return envDir
	}
	return filepath.Join(home, ".kandel")
}

// versionFile es la ruta del marcador de versión de los assets instalados (§21 Eje 3). Vive bajo
// ~/.config/opencode/kandel/VERSION, así el uninstall (que borra esa carpeta) lo retira solo.
func (o opencode) versionFile(home string) string {
	return filepath.Join(o.configDir(home), "kandel", "VERSION")
}

// opencodeRulesBlock es el protocolo de memoria que kandel añade a AGENTS.md como regla always-on.
const opencodeRulesBlock = `## Memoria persistente kandel (protocolo activo)

Tienes memoria persistente entre sesiones mediante las herramientas MCP del servidor **kandel**
(mem_save, mem_search, mem_context, mem_get, mem_update, mem_handoff_get, mem_handoff_save, mem_handoff_resume, …).
El almacén es COMPARTIDO con cualquier otro agente que use kandel en este mismo proyecto (Claude Code, Antigravity, Pi, OpenCode).

Reglas:
- El contexto reciente del proyecto, las memorias relevantes por turno (Capa 2) y el contexto tras
  compactar se **inyectan automáticamente** vía el plugin de kandel; no llames a mem_context solo para
  ver lo reciente. Usa **mem_search** para ir más allá de lo inyectado.
- **Guarda de forma proactiva** con **mem_save** —sin esperar a que te lo pidan— tras: una decisión, un
  arreglo de bug (con su causa raíz), una convención, un descubrimiento no obvio, un patrón, o una
  preferencia/rechazo del usuario.
- Si una memoria quedó obsoleta y conoces su id, usa **mem_update** en vez de crear otra.
- Para relevo entre sesiones o agentes, guarda un checkpoint con **mem_handoff_save** usando el
  session_id actual. Al retomar, lista con **mem_handoff_get**, vincula con **mem_handoff_resume** y
  contrasta siempre el reporte con Git y las pruebas.
- Guarda en el momento en que ocurre lo que vale la pena recordar, no al terminar.
- El protocolo completo (qué guardar, cómo clasificar, la bitácora) está en la skill **kandel-memory**:
  cárgala con la tool ` + "`skill`" + ` cuando vayas a guardar o buscar memoria.`

func (o opencode) Install(home, version string, out io.Writer) error {
	_, mcpFile, rulesFile, skillFile, seedSkillFile, pluginFile := o.paths(home)

	// 1. Skills (directorio kandel-memory para coincidir con el frontmatter `name`).
	if err := writeAsset("assets/skill/SKILL.md", skillFile, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ skill escrita en %s\n", skillFile)

	if err := writeAsset("assets/skill-seed/SKILL.md", seedSkillFile, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ skill de seed escrita en %s\n", seedSkillFile)

	// 2. Plugin V2 de ciclo de vida en ~/.config/opencode/plugins/kandel.js.
	if err := writeAsset("assets/opencode/kandel.js", pluginFile, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ plugin V2 de hooks escrito en %s\n", pluginFile)

	// 3. Registro del servidor MCP en ~/.config/opencode/opencode.json (esquema V2: mcp.servers.kandel).
	if err := o.installMCP(mcpFile); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ servidor MCP 'kandel' registrado en %s\n", mcpFile)

	// 4. Protocolo de memoria en ~/.config/opencode/AGENTS.md.
	if err := mergeRulesBlock(rulesFile, opencodeRulesBlock); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ protocolo de memoria añadido a %s\n", rulesFile)

	// 5. Config local (~/.kandel/config o $KANDEL_DATA_DIR/config): agente activo (first-install-wins) + installed_agents (§22.D).
	confDir := o.dataDir(home)
	if strings.TrimSpace(config.Get(confDir, config.AgentCLI.File)) == "" {
		if err := config.Save(confDir, map[string]string{
			config.AgentCLI.File:  opencodeAgentCLI,
			config.SeedModel.File: llm.SeedModelFor(opencodeAgentCLI),
		}); err != nil {
			return err
		}
	}
	if err := config.AddAgent(confDir, opencodeAgentCLI); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ config de agente escrita en %s (activo: %s)\n",
		filepath.Join(confDir, "config"), config.Get(confDir, config.AgentCLI.File))

	// 6. Marca de versión de los assets (§21 Eje 3).
	vFile := o.versionFile(home)
	if err := writeTextFile(vFile, version, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ versión de assets (%s) marcada en %s\n", version, vFile)

	fmt.Fprintln(out, "\nListo. Reinicia OpenCode para que tome la configuración.")
	fmt.Fprintln(out, "Las memorias se comparten con los demás agentes de este proyecto (Claude Code, Antigravity, Pi).")
	fmt.Fprintln(out, "Requisito: Ollama corriendo con el modelo de embeddings (por defecto qwen3-embedding:0.6b).")
	fmt.Fprintln(out, "  Si falta: ollama pull qwen3-embedding:0.6b")
	return nil
}

func (o opencode) Uninstall(home string, out io.Writer) error {
	ocDir, mcpFile, rulesFile, _, _, pluginFile := o.paths(home)

	// 1. Quita el servidor MCP.
	if err := o.uninstallMCP(mcpFile); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ servidor MCP 'kandel' retirado de %s\n", mcpFile)

	// 2. Quita el bloque de kandel de AGENTS.md sin tocar el resto de las reglas.
	if err := stripRulesBlock(rulesFile); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ protocolo de memoria retirado de %s\n", rulesFile)

	// 3. Borra la carpeta de kandel (incluye VERSION), las skills y el plugin.
	if err := os.RemoveAll(filepath.Join(ocDir, "kandel")); err != nil {
		return fmt.Errorf("borrar carpeta de kandel: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(ocDir, "skills", "kandel-memory")); err != nil {
		return fmt.Errorf("borrar skill: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(ocDir, "skills", "kandel-seed")); err != nil {
		return fmt.Errorf("borrar skill de seed: %w", err)
	}
	if err := os.Remove(pluginFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("borrar plugin de hooks: %w", err)
	}
	fmt.Fprintln(out, "✓ carpeta de kandel, skills y plugin borrados")

	// 4. Quita el agente del registro de instalados y, si era el activo, reasigna al que quede (§22.D).
	confDir := o.dataDir(home)
	if err := config.RemoveAgent(confDir, opencodeAgentCLI); err != nil {
		return err
	}
	if strings.TrimSpace(config.Get(confDir, config.AgentCLI.File)) == opencodeAgentCLI {
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
func (o opencode) InstalledVersion(home string) (string, bool, error) {
	data, err := os.ReadFile(o.versionFile(home))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("leer versión de assets: %w", err)
	}
	return strings.TrimSpace(string(data)), true, nil
}

// installMCP registra kandel bajo el esquema OpenCode V2 (`mcp.servers.kandel`) y limpia cualquier
// entrada legacy V1 (`mcp.kandel`) si existiera en opencode.json.
func (opencode) installMCP(mcpFile string) error {
	cfg, err := loadJSONObject(mcpFile)
	if err != nil {
		return err
	}
	mcpCfg := mapAt(cfg, "mcp")
	delete(mcpCfg, "kandel")
	servers := mapAt(mcpCfg, "servers")
	servers["kandel"] = map[string]any{
		"type":     "local",
		"command":  []any{"kandel", "mcp", "--tools=agent", "--source=opencode"},
		"disabled": false,
	}
	return saveJSONObject(mcpFile, cfg)
}

// uninstallMCP retira `mcp.servers.kandel` (y cualquier `mcp.kandel` legacy V1) sin tocar otros
// servidores ni claves ajenas.
func (opencode) uninstallMCP(mcpFile string) error {
	cfg, err := loadJSONObject(mcpFile)
	if err != nil {
		return err
	}
	mcpCfg, ok := cfg["mcp"].(map[string]any)
	if !ok {
		return nil
	}
	delete(mcpCfg, "kandel")
	if servers, ok := mcpCfg["servers"].(map[string]any); ok {
		delete(servers, "kandel")
		if len(servers) == 0 {
			delete(mcpCfg, "servers")
		}
	}
	if len(mcpCfg) == 0 {
		delete(cfg, "mcp")
	}
	return saveJSONObject(mcpFile, cfg)
}
