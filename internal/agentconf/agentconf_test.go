package agentconf

import (
	"path/filepath"
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
	if err := Save(dir, map[string]string{KeyAgentCLI: "claude", KeySeedModel: "sonnet"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	kv, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if kv[KeyAgentCLI] != "claude" || kv[KeySeedModel] != "sonnet" {
		t.Errorf("round-trip inesperado: %+v", kv)
	}
}

func TestSaveMergesPreservingOtherKeys(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, map[string]string{KeyAgentCLI: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, map[string]string{KeySeedModel: "opus"}); err != nil {
		t.Fatal(err)
	}
	kv, _ := Load(dir)
	if kv[KeyAgentCLI] != "claude" {
		t.Errorf("Save no preservó la clave previa: %+v", kv)
	}
	if kv[KeySeedModel] != "opus" {
		t.Errorf("Save no aplicó la clave nueva: %+v", kv)
	}
}

func TestGetReturnsValue(t *testing.T) {
	dir := t.TempDir()
	_ = Save(dir, map[string]string{KeyAgentCLI: "gemini"})
	if got := Get(dir, KeyAgentCLI); got != "gemini" {
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
