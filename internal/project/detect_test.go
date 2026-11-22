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
	wantDir := Normalize(filepath.Base(root))
	if got.DirName != wantDir {
		t.Errorf("dirName = %q, se esperaba %q", got.DirName, wantDir)
	}

	// Desde una subcarpeta dentro del repo, DirName sigue siendo la carpeta raíz con .git (y no la subcarpeta).
	sub := filepath.Join(root, "internal", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	gotSub := Detect(sub)
	if gotSub.Project != "my-cool-repo" || gotSub.DirName != wantDir {
		t.Errorf("en subcarpeta: project=%q dirName=%q, se esperaba my-cool-repo y %q", gotSub.Project, gotSub.DirName, wantDir)
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

func TestNormalizeProjectsMulti(t *testing.T) {
	got := NormalizeProjects(" Yaku-Web , yaku-servidor , yaku-web , , ")
	if got != "yaku-servidor,yaku-web" {
		t.Errorf("NormalizeProjects = %q, se esperaba yaku-servidor,yaku-web", got)
	}
	if empty := NormalizeProjects(" , , "); empty != "" {
		t.Errorf("NormalizeProjects vacío = %q, se esperaba vacío", empty)
	}
}

func TestContainsAndOverlapProjects(t *testing.T) {
	csv := "yaku-servidor,yaku-web"
	if !ContainsProject(csv, "Yaku-Web") || !ContainsProject(csv, "yaku-servidor") {
		t.Errorf("ContainsProject debía encontrar ambos en %q", csv)
	}
	if ContainsProject(csv, "yaku") || ContainsProject(csv, "kandel") || ContainsProject(csv, "") {
		t.Errorf("ContainsProject no debe coincidir por prefijo ni proyecto ajeno en %q", csv)
	}
	if !OverlapProjects("yaku-web", "yaku-servidor,yaku-web") {
		t.Error("OverlapProjects debía detectar intersección")
	}
	if OverlapProjects("yaku-web,yaku-servidor", "kandel,borrador") {
		t.Error("OverlapProjects no debe detectar intersección entre conjuntos disjuntos")
	}
}
