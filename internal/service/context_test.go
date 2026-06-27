package service

import (
	"context"
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

func TestContextFormatEmpty(t *testing.T) {
	svc := NewContextService(newFakeSessions(), newFakeObs(), newFakePrompts())
	out, err := svc.Format(context.Background(), "kandel", domain.Scope(""))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if out != "" {
		t.Errorf("sin datos debe devolver cadena vacía, fue %q", out)
	}
}

func TestContextFormatPopulated(t *testing.T) {
	ctx := context.Background()
	sessions := newFakeSessions()
	sessions.Create(ctx, domain.Session{ID: "s1", Project: "kandel", Summary: "trabajo previo"})

	memories := newFakeObs()
	memories.Add(ctx, domain.Memory{Project: "kandel", Type: domain.TypeDecision, Title: "Usar JWT", Content: "decisión de auth"})

	prompts := newFakePrompts()
	prompts.Add(ctx, domain.Prompt{Project: "kandel", Content: "implementa login"})

	svc := NewContextService(sessions, memories, prompts)
	out, err := svc.Format(ctx, "Kandel", domain.Scope(""))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	for _, want := range []string{"Sesiones recientes", "trabajo previo", "Prompts recientes", "implementa login", "Memorias recientes", "Usar JWT"} {
		if !strings.Contains(out, want) {
			t.Errorf("el contexto no contiene %q:\n%s", want, out)
		}
	}
}
