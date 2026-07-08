package project

import (
	"os"
	"os/exec"
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
	if got.Project != Normalize(filepath.Base(root)) {
		t.Errorf("project = %q, se esperaba %q", got.Project, Normalize(filepath.Base(root)))
	}
}

func TestDetectGitRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git no disponible")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if err := exec.Command("git", append([]string{"-C", root}, args...)...).Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init")
	run("remote", "add", "origin", "https://github.com/acme/My-Cool-Repo.git")
	// El nombre del remoto gana sobre el basename de la carpeta (temp).
	got := Detect(root)
	if got.Source != SourceGitRemote {
		t.Fatalf("source = %q, se esperaba git_remote", got.Source)
	}
	if got.Project != "my-cool-repo" {
		t.Errorf("project = %q, se esperaba my-cool-repo", got.Project)
	}
}

func TestDetectDirBasename(t *testing.T) {
	// Directorio sin .git en ninguna parte de su cadena propia.
	dir := t.TempDir()
	got := Detect(dir)
	if got.Source != SourceDirBasename {
		t.Errorf("source = %q, se esperaba dir_basename", got.Source)
	}
	if got.Project != Normalize(filepath.Base(dir)) {
		t.Errorf("project = %q, se esperaba %q", got.Project, Normalize(filepath.Base(dir)))
	}
}

func TestDetectEmpty(t *testing.T) {
	got := Detect("")
	if got.Source != SourceNone || got.Project != "" {
		t.Errorf("dir vacío debe dar SourceNone sin proyecto: %+v", got)
	}
}

func TestNormalizeCollapsesSeparators(t *testing.T) {
	if got := Normalize("  My--Cool__Repo  "); got != "my-cool_repo" {
		t.Errorf("Normalize = %q, se esperaba my-cool_repo", got)
	}
}
