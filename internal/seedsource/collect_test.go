package seedsource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectGathersRootAndDocs(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "README.md"), "# Proyecto\nUsa Postgres.")
	mustWrite(t, filepath.Join(dir, "CLAUDE.md"), "Convenciones: español.")
	mustWrite(t, filepath.Join(dir, "go.mod"), "module ejemplo\n\ngo 1.26")
	mustWrite(t, filepath.Join(dir, "docs", "arquitectura.md"), "Hexagonal: domain/ports/service.")
	mustWrite(t, filepath.Join(dir, "ignorame.txt"), "no es markdown ni manifest")

	out, err := Collect(context.Background(), dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, want := range []string{"Postgres", "Convenciones", "module ejemplo", "Hexagonal", "ESTRUCTURA"} {
		if !strings.Contains(out, want) {
			t.Errorf("la salida no incluye %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no es markdown") {
		t.Errorf("no debe incluir archivos no-markdown sueltos")
	}
}

func TestCollectMissingDirFails(t *testing.T) {
	if _, err := Collect(context.Background(), filepath.Join(t.TempDir(), "no-existe")); err == nil {
		t.Fatal("se esperaba error por directorio inexistente")
	}
}

func TestCollectEmptyDirIsEmpty(t *testing.T) {
	out, err := Collect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	// Un directorio vacío puede traer a lo sumo la estructura (vacía); no debe contener documentos.
	if strings.Contains(out, "DOC raíz") || strings.Contains(out, "MANIFEST") {
		t.Errorf("un directorio vacío no debe traer documentos: %q", out)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
