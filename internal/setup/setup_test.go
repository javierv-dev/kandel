package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
	for _, s := range []string{"session-start.sh", "user-prompt-submit.sh", "session-stop.sh", "subagent-stop.sh", "post-compaction.sh"} {
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
	// El env debe habilitar el juez LLM de mem_compare vía la CLI de Claude.
	env, _ := kandelSrv["env"].(map[string]any)
	if env["KANDEL_AGENT_CLI"] != "claude" {
		t.Errorf("falta env KANDEL_AGENT_CLI=claude en el servidor MCP: %+v", kandelSrv)
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
