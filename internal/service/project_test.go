package service

import (
	"context"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
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

func TestProjectMigratorMigrate(t *testing.T) {
	ctx := context.Background()
	memories := newFakeObs()
	memories.Add(ctx, domain.Memory{Project: "viejo", Title: "m"})
	sessions := newFakeSessions()
	sessions.Create(ctx, domain.Session{ID: "s1", Project: "viejo"})
	prompts := newFakePrompts()
	prompts.Add(ctx, domain.Prompt{Project: "viejo", Content: "p"})

	mig := NewProjectMigrator(memories, sessions, prompts, passthroughUoW{})
	res, err := mig.Migrate(ctx, "Viejo", "nuevo")
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !res.Migrated || res.MemoriesUpdated != 1 || res.SessionsUpdated != 1 || res.PromptsUpdated != 1 {
		t.Fatalf("resultado inesperado: %+v", res)
	}
	if got, _ := memories.Recent(ctx, ports.RecentQuery{Project: "nuevo", Limit: 10}); len(got) != 1 {
		t.Errorf("la memoria no se renombró: %d bajo 'nuevo'", len(got))
	}
}

func TestProjectMigratorNoopWhenEqual(t *testing.T) {
	mig := NewProjectMigrator(newFakeObs(), newFakeSessions(), newFakePrompts(), passthroughUoW{})
	// Difieren solo en mayúsculas → tras normalizar son iguales → no migra.
	res, err := mig.Migrate(context.Background(), "Kandel", "kandel")
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if res.Migrated {
		t.Errorf("nombres equivalentes no deben migrar: %+v", res)
	}
}

func TestProjectMigratorNoopWhenEmpty(t *testing.T) {
	mig := NewProjectMigrator(newFakeObs(), newFakeSessions(), newFakePrompts(), passthroughUoW{})
	res, _ := mig.Migrate(context.Background(), "", "nuevo")
	if res.Migrated {
		t.Errorf("nombre viejo vacío no debe migrar: %+v", res)
	}
}
