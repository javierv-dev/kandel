package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiInstall(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	home := t.TempDir()
	if err := Install("pi", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install(pi): %v", err)
	}

	piDir := filepath.Join(home, ".pi", "agent")

	// Skills.
	if _, err := os.Stat(filepath.Join(piDir, "skills", "kandel", "SKILL.md")); err != nil {
		t.Errorf("falta la skill principal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(piDir, "skills", "kandel-seed", "SKILL.md")); err != nil {
		t.Errorf("falta la skill de seed: %v", err)
	}

	// Extensión de ciclo de vida.
	extData, err := os.ReadFile(filepath.Join(piDir, "extensions", "kandel.ts"))
	if err != nil {
		t.Fatalf("falta la extensión kandel.ts: %v", err)
	}
	for _, want := range []string{"pi-session-start", "pi-prompt", "pi-post-compaction", "pi-stop"} {
		if !bytes.Contains(extData, []byte(want)) {
			t.Errorf("kandel.ts no invoca el hook %q", want)
		}
	}

	// MCP: exposure="deferred" y toolExposure con las tools principales en "direct".
	mcp := readJSON(t, filepath.Join(piDir, "mcp.json"))
	servers, _ := mcp["mcpServers"].(map[string]any)
	kandelSrv, ok := servers["kandel"].(map[string]any)
	if !ok {
		t.Fatalf("no se registró el servidor MCP kandel: %+v", servers)
	}
	if kandelSrv["command"] != "kandel" {
		t.Errorf("command = %v, se esperaba kandel", kandelSrv["command"])
	}
	if kandelSrv["exposure"] != "deferred" {
		t.Errorf("exposure = %v, se esperaba deferred", kandelSrv["exposure"])
	}
	toolExp, ok := kandelSrv["toolExposure"].(map[string]any)
	if !ok || toolExp["mem_save"] != "direct" || toolExp["mem_handoff_*"] != "direct" {
		t.Errorf("toolExposure incorrecto: %+v", kandelSrv["toolExposure"])
	}
	if _, ok := kandelSrv["env"]; ok {
		t.Errorf("el servidor MCP no debe fijar env de kandel: %+v", kandelSrv)
	}

	// Config local del agente (first-install-wins: pi queda activo y con su seed_model).
	confData, err := os.ReadFile(filepath.Join(home, ".kandel", "config"))
	if err != nil {
		t.Fatalf("falta el config de agente: %v", err)
	}
	if !strings.Contains(string(confData), "agent_cli=pi") {
		t.Errorf("el config no fija agent_cli=pi:\n%s", confData)
	}

	// Marcador de versión.
	if got, present, err := InstalledVersion("pi", home); err != nil || !present || got != "v9.9.9" {
		t.Errorf("InstalledVersion(pi) = %q present=%v err=%v, se esperaba v9.9.9", got, present, err)
	}
}

func TestPiInstallIdempotentAndUninstallPreservesUserConfig(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	home := t.TempDir()
	piDir := filepath.Join(home, ".pi", "agent")
	mcpFile := filepath.Join(piDir, "mcp.json")
	otherExt := filepath.Join(piDir, "extensions", "otra.ts")

	// Config previa del usuario: otro servidor MCP y otra extensión.
	if err := saveJSONObject(mcpFile, map[string]any{
		"mcpServers": map[string]any{"otro": map[string]any{"command": "otro"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeTextFile(otherExt, "export default function() {}\n", 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Install("pi", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install 1: %v", err)
	}
	if err := Install("pi", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install 2: %v", err)
	}

	if err := Uninstall("pi", home, &bytes.Buffer{}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	// El servidor MCP propio y la otra extensión sobreviven; los de kandel se fueron.
	mcp := readJSON(t, mcpFile)
	servers := mcp["mcpServers"].(map[string]any)
	if _, ok := servers["otro"]; !ok {
		t.Errorf("el servidor MCP propio del usuario no debe borrarse: %+v", servers)
	}
	if _, ok := servers["kandel"]; ok {
		t.Errorf("el servidor MCP de kandel debe retirarse: %+v", servers)
	}
	if _, err := os.Stat(otherExt); err != nil {
		t.Errorf("la extensión ajena debe conservarse: %v", err)
	}
	if _, err := os.Stat(filepath.Join(piDir, "extensions", "kandel.ts")); !os.IsNotExist(err) {
		t.Errorf("la extensión kandel.ts debe retirarse")
	}
	if _, present, _ := InstalledVersion("pi", home); present {
		t.Errorf("tras desinstalar no debe quedar marca de versión")
	}
}
