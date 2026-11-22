package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/config"
)

// ocPaths devuelve las rutas de los artefactos de OpenCode bajo un home de prueba.
func ocPaths(home string) (mcpFile, rulesFile string) {
	return filepath.Join(home, ".config", "opencode", "opencode.json"),
		filepath.Join(home, ".config", "opencode", "AGENTS.md")
}

func TestOpenCodeInstall(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	if err := Install("opencode", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	mcpFile, rulesFile := ocPaths(home)

	// MCP V2: servidor bajo mcp.servers.kandel con type:local, command array, --source=opencode, disabled:false y sin env.
	mcp := readJSON(t, mcpFile)
	mcpCfg, _ := mcp["mcp"].(map[string]any)
	if _, legacy := mcpCfg["kandel"]; legacy {
		t.Errorf("no debe registrarse la clave legacy V1 mcp.kandel: %+v", mcpCfg)
	}
	servers, _ := mcpCfg["servers"].(map[string]any)
	kandelSrv, ok := servers["kandel"].(map[string]any)
	if !ok {
		t.Fatalf("no se registró el servidor MCP en mcp.servers.kandel: %+v", mcpCfg)
	}
	if kandelSrv["type"] != "local" {
		t.Errorf("type del servidor MCP = %v, se esperaba local", kandelSrv["type"])
	}
	cmd, _ := kandelSrv["command"].([]any)
	if len(cmd) != 4 || cmd[0] != "kandel" || cmd[1] != "mcp" || cmd[2] != "--tools=agent" || cmd[3] != "--source=opencode" {
		t.Errorf("command del servidor MCP = %v, se esperaba [kandel mcp --tools=agent --source=opencode]", cmd)
	}
	if disabled, ok := kandelSrv["disabled"].(bool); !ok || disabled {
		t.Errorf("disabled del servidor MCP = %v (present=%v), se esperaba false", kandelSrv["disabled"], ok)
	}
	if _, legacyEnabled := kandelSrv["enabled"]; legacyEnabled {
		t.Errorf("V2 no debe escribir enabled: %+v", kandelSrv)
	}
	if _, ok := kandelSrv["env"]; ok {
		t.Errorf("el servidor MCP no debe fijar env de kandel (Opción X): %+v", kandelSrv)
	}

	// Reglas: AGENTS.md contiene el bloque de kandel con sus marcadores y el protocolo.
	rules, err := os.ReadFile(rulesFile)
	if err != nil {
		t.Fatalf("falta AGENTS.md: %v", err)
	}
	for _, want := range []string{kandelRulesStart, kandelRulesEnd, "mem_save", "mem_handoff_save"} {
		if !bytes.Contains(rules, []byte(want)) {
			t.Errorf("AGENTS.md no contiene %q", want)
		}
	}

	// Skills escritas en el directorio que coincide con el frontmatter name (kandel-memory).
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "skills", "kandel-memory", "SKILL.md")); err != nil {
		t.Errorf("falta la skill kandel-memory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "skills", "kandel-seed", "SKILL.md")); err != nil {
		t.Errorf("falta la skill de seed: %v", err)
	}

	// Plugin V2 de hooks de ciclo de vida.
	pluginFile := filepath.Join(home, ".config", "opencode", "plugins", "kandel.js")
	plugin, err := os.ReadFile(pluginFile)
	if err != nil {
		t.Errorf("falta el plugin de hooks: %v", err)
	} else {
		for _, want := range []string{
			`export default`,
			`id: "kandel"`,
			`async setup(ctx)`,
			`session.created`,
			`session.compaction.ended`,
			`session.deleted`,
			`oc-session-start`,
			`oc-prompt`,
			`oc-post-compaction`,
			`oc-stop`,
		} {
			if !bytes.Contains(plugin, []byte(want)) {
				t.Errorf("el plugin V2 no contiene %q", want)
			}
		}
	}

	// Marcador de versión.
	if got, present, err := InstalledVersion("opencode", home); err != nil || !present || got != "v9.9.9" {
		t.Errorf("versión instalada = %q present=%v err=%v, se esperaba v9.9.9", got, present, err)
	}

	// OpenCode FIJA agent_cli (first-install-wins) y se añade a installed_agents.
	confData, err := os.ReadFile(filepath.Join(home, ".kandel", "config"))
	if err != nil {
		t.Errorf("falta el config de agente: %v", err)
	} else if !bytes.Contains(confData, []byte("agent_cli=opencode")) {
		t.Errorf("el config no fija agent_cli=opencode: %s", confData)
	}
}

func TestOpenCodeInstallMigratesV1MCP(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	mcpFile, _ := ocPaths(home)

	// Simula una config previa con entrada V1 `mcp.kandel` y otro servidor V2 de usuario.
	if err := saveJSONObject(mcpFile, map[string]any{
		"mcp": map[string]any{
			"kandel": map[string]any{"type": "local", "command": []any{"kandel", "mcp"}, "enabled": true},
			"servers": map[string]any{
				"otro": map[string]any{"type": "local", "command": []any{"otro"}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := Install("opencode", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	mcp := readJSON(t, mcpFile)
	mcpCfg := mcp["mcp"].(map[string]any)
	if _, ok := mcpCfg["kandel"]; ok {
		t.Errorf("Install V2 debía limpiar la entrada legacy mcp.kandel: %+v", mcpCfg)
	}
	servers := mcpCfg["servers"].(map[string]any)
	if _, ok := servers["otro"]; !ok {
		t.Errorf("el servidor MCP propio del usuario debe sobrevivir: %+v", servers)
	}
	if _, ok := servers["kandel"]; !ok {
		t.Errorf("debe registrarse mcp.servers.kandel: %+v", servers)
	}
}

func TestOpenCodeInstallIdempotent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	if err := Install("opencode", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install 1: %v", err)
	}
	if err := Install("opencode", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install 2: %v", err)
	}
	mcpFile, rulesFile := ocPaths(home)
	data, err := os.ReadFile(rulesFile)
	if err != nil {
		t.Fatalf("leer AGENTS.md: %v", err)
	}
	if n := strings.Count(string(data), kandelRulesStart); n != 1 {
		t.Errorf("instalar dos veces no debe duplicar el bloque de kandel: %d marcadores", n)
	}

	mcp := readJSON(t, mcpFile)
	mcpCfg := mcp["mcp"].(map[string]any)
	servers := mcpCfg["servers"].(map[string]any)
	if len(servers) != 1 {
		t.Errorf("instalar dos veces no debe duplicar servidores MCP: %d entradas", len(servers))
	}
}

func TestOpenCodeUninstallPreservesUserConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	mcpFile, rulesFile := ocPaths(home)
	otherPlugin := filepath.Join(home, ".config", "opencode", "plugins", "otro.js")

	// El usuario ya tenía un servidor MCP propio, otro plugin y sus propias reglas globales.
	if err := saveJSONObject(mcpFile, map[string]any{
		"mcp": map[string]any{
			"servers": map[string]any{
				"otro": map[string]any{"type": "local", "command": []any{"otro"}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	const userRules = "# Mis reglas\n\nUsa siempre TypeScript estricto."
	if err := writeTextFile(rulesFile, userRules, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeTextFile(otherPlugin, "export default { id: 'otro', setup() {} };\n", 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Install("opencode", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := Uninstall("opencode", home, &bytes.Buffer{}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	// El servidor propio y el otro plugin sobreviven; los de kandel se fueron.
	mcp := readJSON(t, mcpFile)
	mcpCfg := mcp["mcp"].(map[string]any)
	servers := mcpCfg["servers"].(map[string]any)
	if _, ok := servers["otro"]; !ok {
		t.Errorf("el servidor MCP propio del usuario no debe borrarse: %+v", servers)
	}
	if _, ok := servers["kandel"]; ok {
		t.Errorf("el servidor MCP de kandel debe retirarse: %+v", servers)
	}
	if _, err := os.Stat(otherPlugin); err != nil {
		t.Errorf("el plugin ajeno debe conservarse: %v", err)
	}

	// Las reglas propias sobreviven; el bloque de kandel se fue.
	data, err := os.ReadFile(rulesFile)
	if err != nil {
		t.Fatalf("AGENTS.md debe conservarse con las reglas propias: %v", err)
	}
	if !strings.Contains(string(data), "Usa siempre TypeScript estricto") {
		t.Errorf("las reglas propias del usuario deben sobrevivir: %s", data)
	}
	if strings.Contains(string(data), kandelRulesStart) {
		t.Errorf("el bloque de kandel debe retirarse: %s", data)
	}

	if _, present, _ := InstalledVersion("opencode", home); present {
		t.Errorf("tras desinstalar no debe quedar marca de versión")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "skills", "kandel-memory")); !os.IsNotExist(err) {
		t.Errorf("la skill kandel-memory debe borrarse")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "skills", "kandel-seed")); !os.IsNotExist(err) {
		t.Errorf("la skill de seed debe borrarse")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "plugins", "kandel.js")); !os.IsNotExist(err) {
		t.Errorf("el plugin de hooks debe borrarse al desinstalar")
	}
}

func TestOpenCodeUninstallRemovesLoneRulesFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	_, rulesFile := ocPaths(home)

	if err := Install("opencode", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := Uninstall("opencode", home, &bytes.Buffer{}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(rulesFile); !os.IsNotExist(err) {
		t.Errorf("un AGENTS.md que solo tenía el bloque de kandel debe borrarse, no quedar vacío")
	}
}

func TestOpenCodeInstallFirstAgentWins(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	confDir := filepath.Join(home, ".kandel")

	if err := config.Save(confDir, map[string]string{config.AgentCLI.File: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := config.AddAgent(confDir, "claude"); err != nil {
		t.Fatal(err)
	}

	if err := Install("opencode", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if got := config.Get(confDir, config.AgentCLI.File); got != "claude" {
		t.Errorf("agent_cli = %q, se esperaba claude (no debe pisarse)", got)
	}
	installed := config.Agents(confDir)
	if len(installed) != 2 || installed[0] != "claude" || installed[1] != "opencode" {
		t.Errorf("installed_agents = %v, se esperaba [claude opencode]", installed)
	}
}

func TestOpenCodeUninstallReassignsActiveAgent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	confDir := filepath.Join(home, ".kandel")

	if err := Install("opencode", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := config.AddAgent(confDir, "claude"); err != nil {
		t.Fatal(err)
	}

	if err := Uninstall("opencode", home, &bytes.Buffer{}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	installed := config.Agents(confDir)
	if len(installed) != 1 || installed[0] != "claude" {
		t.Errorf("installed_agents tras uninstall = %v, se esperaba [claude]", installed)
	}
	if got := config.Get(confDir, config.AgentCLI.File); got != "claude" {
		t.Errorf("agent_cli tras uninstall = %q, se esperaba reasignado a claude", got)
	}
}
