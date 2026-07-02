package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingReturnsEmpty(t *testing.T) {
	kv, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load de directorio sin config no debe fallar: %v", err)
	}
	if len(kv) != 0 {
		t.Errorf("se esperaba mapa vacío, hay %d entradas", len(kv))
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, map[string]string{AgentCLI.File: "claude", SeedModel.File: "sonnet"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	kv, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if kv[AgentCLI.File] != "claude" || kv[SeedModel.File] != "sonnet" {
		t.Errorf("round-trip inesperado: %+v", kv)
	}
}

func TestSaveMergesPreservingOtherKeys(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, map[string]string{AgentCLI.File: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, map[string]string{SeedModel.File: "opus"}); err != nil {
		t.Fatal(err)
	}
	kv, _ := Load(dir)
	if kv[AgentCLI.File] != "claude" {
		t.Errorf("Save no preservó la clave previa: %+v", kv)
	}
	if kv[SeedModel.File] != "opus" {
		t.Errorf("Save no aplicó la clave nueva: %+v", kv)
	}
}

func TestGetReturnsValue(t *testing.T) {
	dir := t.TempDir()
	_ = Save(dir, map[string]string{AgentCLI.File: "gemini"})
	if got := Get(dir, AgentCLI.File); got != "gemini" {
		t.Errorf("Get = %q, se esperaba gemini", got)
	}
	if got := Get(dir, "inexistente"); got != "" {
		t.Errorf("Get de clave ausente = %q, se esperaba vacío", got)
	}
}

func TestDefaultDirHonorsDataDirEnv(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "datos")
	t.Setenv("KANDEL_DATA_DIR", custom)
	dir, err := DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir: %v", err)
	}
	if dir != custom {
		t.Errorf("DefaultDir = %q, se esperaba %q", dir, custom)
	}
}

// TestResolvePrecedence verifica la cadena env > archivo > default.
func TestResolvePrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KANDEL_DATA_DIR", dir)

	// 1) Sin env ni archivo → default de la clave.
	t.Setenv(Port.Env, "")
	if got := Resolve(Port); got != Port.Default {
		t.Errorf("sin env ni archivo: Resolve(Port) = %q, se esperaba default %q", got, Port.Default)
	}

	// 2) Archivo fija el valor → gana al default.
	if err := Save(dir, map[string]string{Port.File: "9001"}); err != nil {
		t.Fatal(err)
	}
	if got := Resolve(Port); got != "9001" {
		t.Errorf("con archivo: Resolve(Port) = %q, se esperaba 9001", got)
	}

	// 3) La env pisa al archivo.
	t.Setenv(Port.Env, "7777")
	if got := Resolve(Port); got != "7777" {
		t.Errorf("con env: Resolve(Port) = %q, se esperaba 7777", got)
	}
}

// TestResolveEmptyDefaultKey confirma que una clave con Default vacío devuelve "" cuando no se fija.
func TestResolveEmptyDefaultKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KANDEL_DATA_DIR", dir)
	t.Setenv(AgentCLI.Env, "")
	if got := Resolve(AgentCLI); got != "" {
		t.Errorf("Resolve(AgentCLI) sin fijar = %q, se esperaba vacío", got)
	}
}

// TestRenderDocumentsAllKeysWhenEmpty verifica que el archivo generado sin valores activos comenta
// todas las claves conocidas (superficie descubrible) y ninguna queda activa.
func TestRenderDocumentsAllKeysWhenEmpty(t *testing.T) {
	out := Render(nil)
	for _, k := range Keys {
		if !strings.Contains(out, "# "+k.File+"=") {
			t.Errorf("Render no documenta (comentada) la clave %q:\n%s", k.File, out)
		}
		// No debe aparecer activa (una línea que empieza por "clave=").
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, k.File+"=") {
				t.Errorf("la clave %q quedó activa en un Render vacío: %q", k.File, line)
			}
		}
	}
}

// TestRenderEmitsActiveValues verifica que un valor activo se emite sin comentar y los demás siguen
// comentados.
func TestRenderEmitsActiveValues(t *testing.T) {
	out := Render(map[string]string{AgentCLI.File: "claude"})
	if !strings.Contains(out, "\n"+AgentCLI.File+"=claude\n") {
		t.Errorf("Render no emitió el valor activo agent_cli=claude:\n%s", out)
	}
	if !strings.Contains(out, "# "+Port.File+"=") {
		t.Errorf("Render no dejó comentada una clave sin valor activo:\n%s", out)
	}
}

// TestRenderPreservesUnknownKeys confirma que una clave activa no reconocida se conserva.
func TestRenderPreservesUnknownKeys(t *testing.T) {
	out := Render(map[string]string{"futura_opcion": "x"})
	if !strings.Contains(out, "futura_opcion=x") {
		t.Errorf("Render descartó una clave desconocida:\n%s", out)
	}
}

// TestEnsureFileCreatesOnceAndPreserves verifica que EnsureFile crea el archivo autodocumentado si
// falta y no lo pisa si ya existe.
func TestEnsureFileCreatesOnceAndPreserves(t *testing.T) {
	dir := t.TempDir()
	created, err := EnsureFile(dir)
	if err != nil {
		t.Fatalf("EnsureFile: %v", err)
	}
	if !created {
		t.Fatal("EnsureFile debía crear el archivo la primera vez")
	}
	// Un valor guardado tras EnsureFile debe persistir y no perderse en una segunda EnsureFile.
	if err := Save(dir, map[string]string{AgentCLI.File: "claude"}); err != nil {
		t.Fatal(err)
	}
	created2, err := EnsureFile(dir)
	if err != nil {
		t.Fatalf("EnsureFile 2: %v", err)
	}
	if created2 {
		t.Error("EnsureFile no debía recrear el archivo existente")
	}
	if got := Get(dir, AgentCLI.File); got != "claude" {
		t.Errorf("EnsureFile pisó el valor existente: agent_cli = %q", got)
	}
}
