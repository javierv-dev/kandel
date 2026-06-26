package service

import (
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

func TestResolveForWriteStrict(t *testing.T) {
	p := NewProjectService()

	got, err := p.ResolveForWrite("  My--Proj  ")
	if err != nil {
		t.Fatalf("ResolveForWrite: %v", err)
	}
	if got != "my-proj" {
		t.Errorf("debe normalizar a 'my-proj', fue %q", got)
	}

	if _, err := p.ResolveForWrite("   "); err != domain.ErrAmbiguousProject {
		t.Errorf("escribir sin proyecto debe dar ErrAmbiguousProject, fue %v", err)
	}
}

func TestResolveForReadLenient(t *testing.T) {
	p := NewProjectService()

	if got := p.ResolveForRead("  Proj  "); got != "proj" {
		t.Errorf("debe normalizar a 'proj', fue %q", got)
	}
	if got := p.ResolveForRead(""); got != "" {
		t.Errorf("leer sin proyecto es válido (vacío), fue %q", got)
	}
}
