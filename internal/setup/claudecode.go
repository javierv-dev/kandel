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

func init() { register(claudeCode{}) }

// claudeCode cablea kandel en Claude Code: skill en ~/.claude/skills/kandel/, servidor MCP en
// ~/.claude.json y hooks de ciclo de vida en ~/.claude/settings.json, con los scripts en
// ~/.claude/kandel/scripts/. Es el agente primario (§14.10 del plan).
type claudeCode struct{}

func (claudeCode) Name() string { return "claude-code" }

// claudeAgentCLI es el nombre de la CLI de agente de Claude Code (para el juez y el seed); es el
// valor que se registra en installed_agents y, si es el primero, en agent_cli. El modelo de seed se
// deriva del registro único de llm (llm.SeedModelFor), para no duplicar el default.
const claudeAgentCLI = "claude"

// rutas devuelve las rutas de los artefactos a partir del home del usuario.
func (claudeCode) paths(home string) (claudeDir, skillFile, scriptsDir, mcpFile, settingsFile string) {
	claudeDir = filepath.Join(home, ".claude")
	skillFile = filepath.Join(claudeDir, "skills", "kandel", "SKILL.md")
	scriptsDir = filepath.Join(claudeDir, "kandel", "scripts")
	mcpFile = filepath.Join(home, ".claude.json")
	settingsFile = filepath.Join(claudeDir, "settings.json")
	return
}

// hookScripts asocia cada nombre de script con cómo se registra como hook.
type hookSpec struct {
	event   string // evento de Claude Code (SessionStart, UserPromptSubmit, ...)
	matcher string // matcher del grupo (vacío = sin matcher)
	script  string // nombre del archivo de script
	timeout int    // timeout en segundos
}

// hookSpecs describe el mapeo 1:1 de hooks de kandel para Claude Code.
var hookSpecs = []hookSpec{
	{event: "SessionStart", matcher: "startup|clear", script: "session-start.sh", timeout: 10},
	{event: "SessionStart", matcher: "compact", script: "post-compaction.sh", timeout: 10},
	{event: "UserPromptSubmit", script: "user-prompt-submit.sh", timeout: 5},
	{event: "Stop", script: "session-stop.sh", timeout: 5},
}

// versionFile es la ruta del marcador de versión de los assets instalados (§21 Eje 3). Vive junto
// a los scripts, bajo ~/.claude/kandel/, así el uninstall (que borra esa carpeta) lo retira solo.
func (claudeCode) versionFile(home string) string {
	return filepath.Join(home, ".claude", "kandel", "VERSION")
}

func (c claudeCode) Install(home, version string, out io.Writer) error {
	_, skillFile, scriptsDir, mcpFile, settingsFile := c.paths(home)

	// 1. Skill principal.
	if err := writeAsset("assets/skill/SKILL.md", skillFile, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ skill escrita en %s\n", skillFile)

	// 1b. Skill del seed (/kandel-seed): poblar la memoria de un proyecto existente.
	seedSkillFile := filepath.Join(filepath.Dir(filepath.Dir(skillFile)), "kandel-seed", "SKILL.md")
	if err := writeAsset("assets/skill-seed/SKILL.md", seedSkillFile, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ skill de seed escrita en %s\n", seedSkillFile)

	// 2. Scripts de hook.
	scripts, err := writeAssetTree("assets/scripts", scriptsDir, 0o755)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ %d scripts escritos en %s\n", scripts, scriptsDir)

	// 3. Registro del servidor MCP en ~/.claude.json.
	if err := c.installMCP(mcpFile); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ servidor MCP 'kandel' registrado en %s\n", mcpFile)

	// 4. Hooks en ~/.claude/settings.json.
	if err := c.installHooks(settingsFile, scriptsDir); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ hooks de ciclo de vida fusionados en %s\n", settingsFile)

	// 5. Config local (~/.kandel/config): agente activo + registro de agentes instalados (§22.D).
	// El agente activo (que juzga y siembra) sigue la regla first-install-wins: solo se fija si aún
	// no hay uno, para no pisar la elección previa cuando se instala un segundo agente. El agente
	// siempre se añade al conjunto installed_agents (habilita `kandel agent use` para cambiar/caer).
	confDir := filepath.Join(home, ".kandel")
	if strings.TrimSpace(config.Get(confDir, config.AgentCLI.File)) == "" {
		if err := config.Save(confDir, map[string]string{
			config.AgentCLI.File:  claudeAgentCLI,
			config.SeedModel.File: llm.SeedModelFor(claudeAgentCLI),
		}); err != nil {
			return err
		}
	}
	if err := config.AddAgent(confDir, claudeAgentCLI); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ config de agente escrita en %s (activo: %s)\n",
		filepath.Join(confDir, "config"), config.Get(confDir, config.AgentCLI.File))

	// 6. Marca de versión de los assets (§21 Eje 3): permite detectar más tarde que el binario se
	// actualizó pero los assets quedaron viejos (banner de "reinstala" en el diagnóstico).
	if err := writeTextFile(c.versionFile(home), version, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ versión de assets (%s) marcada en %s\n", version, c.versionFile(home))

	fmt.Fprintln(out, "\nListo. Reinicia Claude Code para que tome la configuración.")
	fmt.Fprintln(out, "Requisito: Ollama corriendo con el modelo de embeddings (por defecto qwen3-embedding:0.6b).")
	fmt.Fprintln(out, "  Si falta: ollama pull qwen3-embedding:0.6b")
	return nil
}

func (c claudeCode) Uninstall(home string, out io.Writer) error {
	claudeDir, _, scriptsDir, mcpFile, settingsFile := c.paths(home)

	// 1. Quita el servidor MCP.
	if err := c.uninstallMCP(mcpFile); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ servidor MCP 'kandel' retirado de %s\n", mcpFile)

	// 2. Quita los hooks de kandel (los que apuntan a su carpeta de scripts).
	if err := c.uninstallHooks(settingsFile, scriptsDir); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ hooks de kandel retirados de %s\n", settingsFile)

	// 3. Borra las skills y los scripts (carpetas propias de kandel).
	if err := os.RemoveAll(filepath.Join(claudeDir, "skills", "kandel")); err != nil {
		return fmt.Errorf("borrar skill: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(claudeDir, "skills", "kandel-seed")); err != nil {
		return fmt.Errorf("borrar skill de seed: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(claudeDir, "kandel")); err != nil {
		return fmt.Errorf("borrar scripts: %w", err)
	}
	fmt.Fprintln(out, "✓ skills y scripts borrados")

	// 4. Quita el agente del registro de instalados y, si era el activo, reasigna al que quede (o lo
	// desconfigura si no queda ninguno) — §22.D.
	confDir := filepath.Join(home, ".kandel")
	if err := config.RemoveAgent(confDir, claudeAgentCLI); err != nil {
		return err
	}
	if strings.TrimSpace(config.Get(confDir, config.AgentCLI.File)) == claudeAgentCLI {
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

// InstalledVersion lee el marcador de versión de los assets. present es false si no existe (agente
// no instalado por esta vía).
func (c claudeCode) InstalledVersion(home string) (string, bool, error) {
	data, err := os.ReadFile(c.versionFile(home))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("leer versión de assets: %w", err)
	}
	return strings.TrimSpace(string(data)), true, nil
}

// installMCP registra el servidor MCP de kandel en mcpServers.kandel.
func (claudeCode) installMCP(mcpFile string) error {
	cfg, err := loadJSONObject(mcpFile)
	if err != nil {
		return err
	}
	servers := mapAt(cfg, "mcpServers")
	// Opción X (§22): kandel NO inyecta su env aquí. El agente activo (para el juez LLM de mem_compare)
	// se resuelve desde ~/.kandel/config (config.AgentCLI), que también lee el daemon. Un único origen
	// de verdad, sin sangrar ajustes de kandel en el archivo de Claude Code.
	servers["kandel"] = map[string]any{
		"command": "kandel",
		"args":    []any{"mcp", "--tools=agent", "--source=claude-code"},
	}
	return saveJSONObject(mcpFile, cfg)
}

// uninstallMCP quita la clave mcpServers.kandel sin tocar otros servidores.
func (claudeCode) uninstallMCP(mcpFile string) error {
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

// installHooks fusiona los hooks de kandel en settings.json. Primero retira cualquier hook previo
// de kandel (idempotencia) y luego añade los actuales apuntando a scriptsDir.
func (c claudeCode) installHooks(settingsFile, scriptsDir string) error {
	settings, err := loadJSONObject(settingsFile)
	if err != nil {
		return err
	}
	removeKandelHooks(settings, scriptsDir)

	hooks := mapAt(settings, "hooks")
	for _, spec := range hookSpecs {
		command := quote(filepath.ToSlash(filepath.Join(scriptsDir, spec.script)))
		inner := map[string]any{"type": "command", "command": command, "timeout": spec.timeout}
		group := map[string]any{"hooks": []any{inner}}
		if spec.matcher != "" {
			group["matcher"] = spec.matcher
		}
		hooks[spec.event] = append(asSlice(hooks[spec.event]), group)
	}
	return saveJSONObject(settingsFile, settings)
}

// uninstallHooks retira de settings.json los grupos cuyos comandos apuntan a scriptsDir.
func (claudeCode) uninstallHooks(settingsFile, scriptsDir string) error {
	settings, err := loadJSONObject(settingsFile)
	if err != nil {
		return err
	}
	removeKandelHooks(settings, scriptsDir)
	return saveJSONObject(settingsFile, settings)
}

// removeKandelHooks elimina, de cada evento de settings["hooks"], los grupos que referencian la
// carpeta de scripts de kandel. Borra los eventos que queden vacíos y la sección "hooks" si queda
// sin nada. Es la base del uninstall quirúrgico: no toca hooks ajenos.
func removeKandelHooks(settings map[string]any, scriptsDir string) {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return
	}
	marker := filepath.ToSlash(scriptsDir)
	for event, raw := range hooks {
		groups := asSlice(raw)
		kept := make([]any, 0, len(groups))
		for _, g := range groups {
			if !groupReferences(g, marker) {
				kept = append(kept, g)
			}
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	}
}

// groupReferences indica si un grupo de hooks contiene algún comando que apunte al marcador dado.
func groupReferences(group any, marker string) bool {
	gm, ok := group.(map[string]any)
	if !ok {
		return false
	}
	for _, h := range asSlice(gm["hooks"]) {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, ok := hm["command"].(string); ok && strings.Contains(cmd, marker) {
			return true
		}
	}
	return false
}

// asSlice normaliza un valor JSON a []any (nil si no es lista).
func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// quote envuelve una ruta en comillas dobles para el campo command del hook (tolera espacios).
func quote(s string) string {
	return `"` + s + `"`
}
