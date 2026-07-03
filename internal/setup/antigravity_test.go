package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agPaths devuelve las rutas de los artefactos de Antigravity bajo un home de prueba.
func agPaths(home string) (mcpFile, rulesFile string) {
	return filepath.Join(home, ".gemini", "config", "mcp_config.json"),
		filepath.Join(home, ".gemini", "GEMINI.md")
}

func TestAntigravityInstall(t *testing.T) {
	home := t.TempDir()
	if err := Install("antigravity", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	mcpFile, rulesFile := agPaths(home)

	// MCP: servidor kandel registrado con el binario único, sin env (mismo criterio Opción X).
	mcp := readJSON(t, mcpFile)
	servers, _ := mcp["mcpServers"].(map[string]any)
	kandelSrv, ok := servers["kandel"].(map[string]any)
	if !ok {
		t.Fatalf("no se registró el servidor MCP kandel: %+v", servers)
	}
	if kandelSrv["command"] != "kandel" {
		t.Errorf("command del servidor MCP = %v, se esperaba kandel", kandelSrv["command"])
	}
	if _, ok := kandelSrv["env"]; ok {
		t.Errorf("el servidor MCP no debe fijar env de kandel: %+v", kandelSrv)
	}

	// Reglas: GEMINI.md contiene el bloque de kandel con sus marcadores y el protocolo.
	rules, err := os.ReadFile(rulesFile)
	if err != nil {
		t.Fatalf("falta GEMINI.md: %v", err)
	}
	for _, want := range []string{kandelRulesStart, kandelRulesEnd, "mem_context", "mem_save"} {
		if !bytes.Contains(rules, []byte(want)) {
			t.Errorf("GEMINI.md no contiene %q", want)
		}
	}

	// Marcador de versión.
	if got, present, err := InstalledVersion("antigravity", home); err != nil || !present || got != "v9.9.9" {
		t.Errorf("versión instalada = %q present=%v err=%v, se esperaba v9.9.9", got, present, err)
	}

	// Hooks: PreInvocation y Stop enganchados con el comando de kandel.
	hooks := readJSON(t, filepath.Join(home, ".gemini", "config", "hooks.json"))
	pre, ok := hooks["kandel-pre-invocation"].(map[string]any)
	if !ok {
		t.Fatalf("no se enganchó el hook PreInvocation de kandel: %+v", hooks)
	}
	handlers, _ := pre["PreInvocation"].([]any)
	if len(handlers) != 1 {
		t.Fatalf("PreInvocation debe tener 1 handler: %+v", pre)
	}
	if cmd, _ := handlers[0].(map[string]any)["command"].(string); cmd != "kandel hook ag-pre-invocation" {
		t.Errorf("comando del hook PreInvocation = %q", cmd)
	}
	if _, ok := hooks["kandel-stop"].(map[string]any); !ok {
		t.Errorf("no se enganchó el hook Stop de kandel: %+v", hooks)
	}

	// Antigravity NO toca la CLI de agente activa (agent_cli).
	if _, err := os.Stat(filepath.Join(home, ".kandel", "config")); !os.IsNotExist(err) {
		t.Errorf("Antigravity no debe tocar el config de agente")
	}
}

func TestAntigravityInstallIdempotent(t *testing.T) {
	home := t.TempDir()
	if err := Install("antigravity", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install 1: %v", err)
	}
	if err := Install("antigravity", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install 2: %v", err)
	}
	_, rulesFile := agPaths(home)
	data, err := os.ReadFile(rulesFile)
	if err != nil {
		t.Fatalf("leer GEMINI.md: %v", err)
	}
	if n := strings.Count(string(data), kandelRulesStart); n != 1 {
		t.Errorf("instalar dos veces no debe duplicar el bloque de kandel: %d marcadores", n)
	}
}

func TestAntigravityUninstallPreservesUserConfig(t *testing.T) {
	home := t.TempDir()
	mcpFile, rulesFile := agPaths(home)

	// El usuario ya tenía un servidor MCP propio y sus propias reglas globales.
	if err := saveJSONObject(mcpFile, map[string]any{
		"mcpServers": map[string]any{"otro": map[string]any{"command": "otro"}},
	}); err != nil {
		t.Fatal(err)
	}
	const userRules = "# Mis reglas\n\nUsa siempre TypeScript estricto."
	if err := writeTextFile(rulesFile, userRules, 0o644); err != nil {
		t.Fatal(err)
	}
	// El usuario ya tenía un hook propio.
	hooksFile := filepath.Join(home, ".gemini", "config", "hooks.json")
	if err := saveJSONObject(hooksFile, map[string]any{
		"mi-linter": map[string]any{"PostToolUse": []any{map[string]any{"matcher": "run_command"}}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := Install("antigravity", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := Uninstall("antigravity", home, &bytes.Buffer{}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	// El hook propio sobrevive; los de kandel se fueron.
	hooks := readJSON(t, hooksFile)
	if _, ok := hooks["mi-linter"]; !ok {
		t.Errorf("el hook propio del usuario no debe borrarse: %+v", hooks)
	}
	if _, ok := hooks["kandel-pre-invocation"]; ok {
		t.Errorf("el hook PreInvocation de kandel debe retirarse: %+v", hooks)
	}
	if _, ok := hooks["kandel-stop"]; ok {
		t.Errorf("el hook Stop de kandel debe retirarse: %+v", hooks)
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

	// Las reglas propias sobreviven; el bloque de kandel se fue.
	data, err := os.ReadFile(rulesFile)
	if err != nil {
		t.Fatalf("GEMINI.md debe conservarse con las reglas propias: %v", err)
	}
	if !strings.Contains(string(data), "Usa siempre TypeScript estricto") {
		t.Errorf("las reglas propias del usuario deben sobrevivir: %s", data)
	}
	if strings.Contains(string(data), kandelRulesStart) {
		t.Errorf("el bloque de kandel debe retirarse: %s", data)
	}

	// El marcador de versión se fue.
	if _, present, _ := InstalledVersion("antigravity", home); present {
		t.Errorf("tras desinstalar no debe quedar marca de versión")
	}
}

// TestAntigravityUninstallRemovesLoneRulesFile: si GEMINI.md solo tenía el bloque de kandel (el usuario
// no tenía reglas propias), el uninstall borra el archivo en vez de dejarlo vacío.
func TestAntigravityUninstallRemovesLoneRulesFile(t *testing.T) {
	home := t.TempDir()
	_, rulesFile := agPaths(home)

	if err := Install("antigravity", home, "v9.9.9", &bytes.Buffer{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := Uninstall("antigravity", home, &bytes.Buffer{}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(rulesFile); !os.IsNotExist(err) {
		t.Errorf("un GEMINI.md que solo tenía el bloque de kandel debe borrarse, no quedar vacío")
	}
}
