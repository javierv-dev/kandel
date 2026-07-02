package auditlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogActionAppendsJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "tui-actions.jsonl")
	l := New(path) // debe crear el subdirectorio que falta

	l.LogAction("soft_delete", 42, "Usar hexagonal")
	l.LogAction("restore", 42, "Usar hexagonal")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no se pudo leer el log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("se esperaban 2 líneas, hubo %d: %q", len(lines), string(data))
	}

	var first struct {
		TS     string `json:"ts"`
		Action string `json:"action"`
		ID     int64  `json:"id"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("la primera línea no es JSON válido: %v", err)
	}
	if first.Action != "soft_delete" || first.ID != 42 || first.Title != "Usar hexagonal" {
		t.Errorf("primera entrada mal registrada: %+v", first)
	}
	if first.TS == "" {
		t.Error("la entrada debe llevar marca de tiempo")
	}
}
