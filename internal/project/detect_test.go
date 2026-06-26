package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectGitRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Desde un subdirectorio, debe subir hasta la raíz con .git y usar su basename.
	got := Detect(sub)
	if got.Source != SourceGitRoot {
		t.Errorf("source = %q, se esperaba git_root", got.Source)
	}
	if got.Project != normalize(filepath.Base(root)) {
		t.Errorf("project = %q, se esperaba %q", got.Project, normalize(filepath.Base(root)))
	}
}

func TestDetectDirBasename(t *testing.T) {
	// Directorio sin .git en ninguna parte de su cadena propia.
	dir := t.TempDir()
	got := Detect(dir)
	if got.Source != SourceDirBasename {
		t.Errorf("source = %q, se esperaba dir_basename", got.Source)
	}
	if got.Project != normalize(filepath.Base(dir)) {
		t.Errorf("project = %q, se esperaba %q", got.Project, normalize(filepath.Base(dir)))
	}
}

func TestDetectEmpty(t *testing.T) {
	got := Detect("")
	if got.Source != SourceNone || got.Project != "" {
		t.Errorf("dir vacío debe dar SourceNone sin proyecto: %+v", got)
	}
}

func TestNormalizeCollapsesSeparators(t *testing.T) {
	if got := normalize("  My--Cool__Repo  "); got != "my-cool_repo" {
		t.Errorf("normalize = %q, se esperaba my-cool_repo", got)
	}
}
