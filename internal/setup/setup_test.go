package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/javierv-dev/kandel/internal/config"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("leer %q: %v", path, err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("parsear %q: %v", path, err)
	}
	return obj
}

func TestClaudeCodeInstall(t *testing.T) {
	home := t.TempDir()
	if err := Install("claude-code", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Skill.
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "kandel", "SKILL.md")); err != nil {
		t.Errorf("falta la skill: %v", err)
	}
	// Skill del seed.
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "kandel-seed", "SKILL.md")); err != nil {
		t.Errorf("falta la skill de seed: %v", err)
	}
	// Config local del agente (para el seed standalone).
	confData, err := os.ReadFile(filepath.Join(home, ".kandel", "config"))
	if err != nil {
		t.Errorf("falta el config de agente: %v", err)
	} else if !bytes.Contains(confData, []byte("agent_cli=claude")) {
		t.Errorf("el config no fija agent_cli=claude: %s", confData)
	}
	// Scripts.
	for _, s := range []string{"session-start.sh", "user-prompt-submit.sh", "session-stop.sh", "post-compaction.sh"} {
		if _, err := os.Stat(filepath.Join(home, ".claude", "kandel", "scripts", s)); err != nil {
			t.Errorf("falta el script %q: %v", s, err)
		}
	}
	// MCP.
	mcp := readJSON(t, filepath.Join(home, ".claude.json"))
	servers, _ := mcp["mcpServers"].(map[string]any)
	kandelSrv, ok := servers["kandel"].(map[string]any)
	if !ok {
		t.Fatalf("no se registró el servidor MCP kandel: %+v", servers)
	}
	// Opción X (§22): el servidor MCP NO debe llevar env de kandel. El agente activo del juez de
	// mem_compare se resuelve desde ~/.kandel/config (agent_cli, verificado arriba), no desde aquí.
	if _, ok := kandelSrv["env"]; ok {
		t.Errorf("el servidor MCP no debe fijar env de kandel (Opción X): %+v", kandelSrv)
	}
	// Hooks.
	settings := readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	hooks, _ := settings["hooks"].(map[string]any)
	if _, ok := hooks["SessionStart"].([]any); !ok {
		t.Errorf("no se registraron hooks SessionStart: %+v", hooks)
	}
	if ss := hooks["SessionStart"].([]any); len(ss) != 2 {
		t.Errorf("SessionStart debe tener 2 grupos (startup|clear, compact), tiene %d", len(ss))
	}
}

func TestClaudeCodeInstallIdempotent(t *testing.T) {
	home := t.TempDir()
	if err := Install("claude-code", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install 1: %v", err)
	}
	if err := Install("claude-code", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install 2: %v", err)
	}
	settings := readJSON(t, filepath.Join(home, ".claude", "settings.json"))
	hooks := settings["hooks"].(map[string]any)
	if ss := hooks["SessionStart"].([]any); len(ss) != 2 {
		t.Errorf("instalar dos veces no debe duplicar hooks: SessionStart tiene %d grupos", len(ss))
	}
	if up := hooks["UserPromptSubmit"].([]any); len(up) != 1 {
		t.Errorf("UserPromptSubmit duplicado: %d grupos", len(up))
	}
}

func TestClaudeCodeUninstallPreservesUserConfig(t *testing.T) {
	home := t.TempDir()

	// El usuario ya tenía un servidor MCP propio y un hook propio.
	mcpFile := filepath.Join(home, ".claude.json")
	if err := saveJSONObject(mcpFile, map[string]any{
		"mcpServers": map[string]any{"otro": map[string]any{"command": "otro"}},
	}); err != nil {
		t.Fatal(err)
	}
	settingsFile := filepath.Join(home, ".claude", "settings.json")
	if err := saveJSONObject(settingsFile, map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "mi-script.sh"}}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := Install("claude-code", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := Uninstall("claude-code", home, &bytes.Buffer{}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	// El servidor propio sobrevive; el de kandel se fue.
	mcp := readJSON(t, mcpFile)
	servers := mcp["mcpServers"].(map[string]any)
	if _, ok := servers["otro"]; !ok {
		t.Errorf("el servidor MCP propio del usuario no debe borrarse: %+v", servers)
	}
	if _, ok := servers["kandel"]; ok {
		t.Errorf("el servidor MCP de kandel debe retirarse: %+v", servers)
	}

	// El hook propio sobrevive; los de kandel se fueron.
	settings := readJSON(t, settingsFile)
	hooks := settings["hooks"].(map[string]any)
	stop, ok := hooks["Stop"].([]any)
	if !ok || len(stop) != 1 {
		t.Fatalf("el hook Stop propio debe sobrevivir: %+v", hooks["Stop"])
	}
	if _, ok := hooks["UserPromptSubmit"]; ok {
		t.Errorf("los hooks de kandel deben retirarse: quedó UserPromptSubmit")
	}

	// Las skills y los scripts se borraron.
	if _, err := os.Stat(filepath.Join(home, ".claude", "kandel")); !os.IsNotExist(err) {
		t.Errorf("la carpeta de scripts de kandel debe borrarse")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "kandel-seed")); !os.IsNotExist(err) {
		t.Errorf("la skill de seed de kandel debe borrarse")
	}
}

func TestInstallUnknownAgent(t *testing.T) {
	if err := Install("inexistente", t.TempDir(), "v9.9.9", &bytes.Buffer{}); err == nil {
		t.Error("un agente no soportado debe dar error")
	}
}

// TestInstallFirstAgentWins verifica §22.D: el primer install fija agent_cli; un install posterior no
// lo pisa si ya hay uno, pero sí añade el agente a installed_agents.
func TestInstallFirstAgentWins(t *testing.T) {
	home := t.TempDir()
	confDir := filepath.Join(home, ".kandel")

	// Simula que otro agente se instaló primero y quedó como activo.
	if err := config.Save(confDir, map[string]string{config.AgentCLI.File: "gemini"}); err != nil {
		t.Fatal(err)
	}
	if err := config.AddAgent(confDir, "gemini"); err != nil {
		t.Fatal(err)
	}

	if err := Install("claude-code", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// El activo NO cambia (first-install-wins); claude se suma al conjunto de instalados.
	if got := config.Get(confDir, config.AgentCLI.File); got != "gemini" {
		t.Errorf("agent_cli = %q, se esperaba gemini (no debe pisarse)", got)
	}
	if got := config.Agents(confDir); len(got) != 2 || got[0] != "claude" || got[1] != "gemini" {
		t.Errorf("installed_agents = %v, se esperaba [claude gemini]", got)
	}
}

// TestUninstallReassignsActiveAgent verifica §22.D: al desinstalar el agente activo, si queda otro
// instalado se reasigna a él; el agente se quita del conjunto.
func TestUninstallReassignsActiveAgent(t *testing.T) {
	home := t.TempDir()
	confDir := filepath.Join(home, ".kandel")

	if err := Install("claude-code", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// claude quedó activo; añadimos un segundo agente al conjunto para que haya reemplazo.
	if err := config.AddAgent(confDir, "gemini"); err != nil {
		t.Fatal(err)
	}

	if err := Uninstall("claude-code", home, &bytes.Buffer{}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if got := config.Agents(confDir); len(got) != 1 || got[0] != "gemini" {
		t.Errorf("installed_agents tras uninstall = %v, se esperaba [gemini]", got)
	}
	if got := config.Get(confDir, config.AgentCLI.File); got != "gemini" {
		t.Errorf("agent_cli tras uninstall = %q, se esperaba reasignado a gemini", got)
	}
}

// TestInstalledVersionMarker verifica el ciclo del marcador de versión (§21 Eje 3): sin instalar no
// hay marca; tras instalar, InstalledVersion devuelve la versión estampada; tras desinstalar, se va.
func TestInstalledVersionMarker(t *testing.T) {
	home := t.TempDir()

	if _, present, err := InstalledVersion("claude-code", home); err != nil || present {
		t.Fatalf("sin instalar no debe haber marca: present=%v err=%v", present, err)
	}

	if err := Install("claude-code", home, "v1.2.3", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	got, present, err := InstalledVersion("claude-code", home)
	if err != nil {
		t.Fatalf("InstalledVersion: %v", err)
	}
	if !present || got != "v1.2.3" {
		t.Fatalf("versión instalada = %q present=%v, se esperaba v1.2.3", got, present)
	}

	if err := Uninstall("claude-code", home, &bytes.Buffer{}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, present, _ := InstalledVersion("claude-code", home); present {
		t.Fatalf("tras desinstalar no debe quedar marca de versión")
	}
}
