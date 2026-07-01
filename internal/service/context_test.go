package service

import (
	"context"
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

func TestContextFormatEmpty(t *testing.T) {
	svc := NewContextService(newFakeSessions(), newFakeObs())
	out, err := svc.Format(context.Background(), "kandel", domain.Scope(""))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if out != "" {
		t.Errorf("sin datos debe devolver cadena vacía, fue %q", out)
	}
}

func TestContextFormatBandejas(t *testing.T) {
	ctx := context.Background()
	sessions := newFakeSessions()
	sessions.Create(ctx, domain.Session{ID: "s1", Project: "kandel", Summary: "trabajo previo"})

	memories := newFakeObs()
	// Normativa (Siempre) sin verificar → alimenta el banner de salud.
	memories.Add(ctx, domain.Memory{Project: "kandel", Type: domain.TypeDecision, Title: "Usar JWT", Content: "decisión de auth"})
	// Pendiente por resolver.
	memories.Add(ctx, domain.Memory{Project: "kandel", Type: domain.TypeDecision, Title: "Elegir cola", Content: "aún sin decidir", Resolution: domain.ResolutionOpen})
	// Factual reciente.
	memories.Add(ctx, domain.Memory{Project: "kandel", Type: domain.TypeDiscovery, Title: "Ollama tarda", Content: "cold start 7s"})

	svc := NewContextService(sessions, memories)
	out, err := svc.Format(ctx, "Kandel", domain.Scope(""))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	for _, want := range []string{
		"### Siempre presente (normativas)", "Usar JWT",
		"### Pendientes por resolver", "Elegir cola",
		"### Recientes", "Ollama tarda",
		"### Sesiones recientes", "trabajo previo",
		"### Salud de memoria", "sin verificar",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("el contexto no contiene %q:\n%s", want, out)
		}
	}
	// Los prompts ya NO se inyectan.
	if strings.Contains(out, "Prompts recientes") {
		t.Errorf("los prompts no deben inyectarse:\n%s", out)
	}
}

func TestContextHealthTooManyNormatives(t *testing.T) {
	ctx := context.Background()
	memories := newFakeObs()
	// Dos normativas verificadas; con umbral 2 dispara el aviso de mantenimiento.
	for _, title := range []string{"A", "B"} {
		memories.Add(ctx, domain.Memory{Project: "kandel", Type: domain.TypeDecision, Title: title, Content: "x", ConflictChecked: true})
	}

	svc := NewContextService(newFakeSessions(), memories, WithNormativeThreshold(2))
	out, err := svc.Format(ctx, "kandel", domain.Scope(""))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if !strings.Contains(out, "demasiadas normativas") {
		t.Errorf("debe avisar de demasiadas normativas:\n%s", out)
	}
	// Verificadas → sin banner "sin verificar".
	if strings.Contains(out, "sin verificar") {
		t.Errorf("normativas verificadas no deben disparar el aviso de sin verificar:\n%s", out)
	}
}
