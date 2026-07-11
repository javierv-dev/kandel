package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

func TestContextFormatEmpty(t *testing.T) {
	svc := NewContextService(newFakeObs(), &fakeRelations{})
	out, err := svc.Format(context.Background(), "kandel", domain.Scope(""))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if out != "" {
		t.Errorf("sin datos debe devolver cadena vacía, fue %q", out)
	}
}

func TestContextFormatMinimo(t *testing.T) {
	ctx := context.Background()
	memories := newFakeObs()
	// Normativa sin verificar → alimenta el banner de salud, pero su CUERPO ya NO se vuelca.
	memories.Add(ctx, domain.Memory{Project: "kandel", Type: domain.TypeDecision, Title: "Usar JWT", Content: "decisión de auth"})
	// Pendiente por resolver (no journal): ya NO se muestra al arranque; llega por relevancia.
	memories.Add(ctx, domain.Memory{Project: "kandel", Type: domain.TypeDecision, Title: "Elegir cola", Content: "aún sin decidir", Resolution: domain.ResolutionOpen})
	// Factual reciente: ya NO se vuelca al arranque.
	memories.Add(ctx, domain.Memory{Project: "kandel", Type: domain.TypeDiscovery, Title: "Ollama tarda", Content: "cold start 7s"})

	svc := NewContextService(memories, &fakeRelations{})
	out, err := svc.Format(ctx, "Kandel", domain.Scope(""))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	// Se conservan: el proyecto normalizado y la salud (higiene, sin coste de tokens de contenido).
	for _, want := range []string{"Proyecto actual: kandel", "### Salud de memoria", "sin verificar"} {
		if !strings.Contains(out, want) {
			t.Errorf("el contexto no contiene %q:\n%s", want, out)
		}
	}
	// Ya NO se vuelcan: normativas, pendientes, recientes ni sus cuerpos.
	for _, unwanted := range []string{
		"### Siempre presente (normativas)", "decisión de auth",
		"### Pendientes por resolver", "Elegir cola",
		"### Recientes", "Ollama tarda", "cold start 7s",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("el arranque mínimo no debe contener %q:\n%s", unwanted, out)
		}
	}
}

func TestContextJournalBandeja(t *testing.T) {
	ctx := context.Background()
	memories := newFakeObs()
	// Entrada de bitácora abierta (estado actual, cursor mutable).
	memories.Add(ctx, domain.Memory{Project: "kandel", Type: domain.TypeJournal, Title: "2026-07-06 — tema en vuelo", Content: "trabajando el journal", Resolution: domain.ResolutionOpen})
	// Entrada de bitácora cerrada (historial).
	memories.Add(ctx, domain.Memory{Project: "kandel", Type: domain.TypeJournal, Title: "2026-07-05 — tema cerrado", Content: "resumen de sesión eliminado", Resolution: domain.ResolutionResolved})

	svc := NewContextService(memories, &fakeRelations{})
	out, err := svc.Format(ctx, "kandel", domain.Scope(""))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	// La bitácora abierta se superficia como TÍTULO (el cursor "dónde nos quedamos").
	for _, want := range []string{"### Bitácora — estado actual", "tema en vuelo"} {
		if !strings.Contains(out, want) {
			t.Errorf("el contexto no contiene %q:\n%s", want, out)
		}
	}
	// El historial cerrado ya NO se inyecta al arranque (llega por relevancia / mem_thread).
	for _, unwanted := range []string{"### Bitácora — historial reciente", "tema cerrado"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("el historial no debe inyectarse al arranque:\n%s", out)
		}
	}
	// Solo el título: el cuerpo de la journal abierta no se vuelca.
	if strings.Contains(out, "trabajando el journal") {
		t.Errorf("el cuerpo de la bitácora no debe volcarse (solo título):\n%s", out)
	}
}

func TestContextJournalBreadcrumb(t *testing.T) {
	ctx := context.Background()
	memories := newFakeObs()
	// Fase anterior (cerrada) y fase actual (abierta) del mismo hilo, con sync_ids estables.
	predID, _ := memories.Add(ctx, domain.Memory{SyncID: "mem-b", Project: "kandel", Type: domain.TypeJournal, Title: "Fase 2", Content: "x", Resolution: domain.ResolutionResolved})
	memories.Add(ctx, domain.Memory{SyncID: "mem-c", Project: "kandel", Type: domain.TypeJournal, Title: "Fase 3", Content: "y", Resolution: domain.ResolutionOpen})

	rels := &fakeRelations{}
	// La fase actual (mem-c) continúa a la anterior (mem-b).
	rels.Insert(ctx, domain.MemoryRelation{SourceID: "mem-c", TargetID: "mem-b", Kind: domain.RelationContinues, Status: domain.JudgmentJudged})

	svc := NewContextService(memories, rels)
	out, err := svc.Format(ctx, "kandel", domain.Scope(""))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if !strings.Contains(out, "Fase 3") {
		t.Fatalf("debía mostrar la bitácora abierta:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("⭱ viene de id %d", predID)) {
		t.Errorf("debía mostrar la miga hacia la fase anterior (id %d):\n%s", predID, out)
	}
}

func TestContextHealthTooManyNormatives(t *testing.T) {
	ctx := context.Background()
	memories := newFakeObs()
	// Dos normativas verificadas; con umbral 2 dispara el aviso de mantenimiento.
	for _, title := range []string{"A", "B"} {
		memories.Add(ctx, domain.Memory{Project: "kandel", Type: domain.TypeDecision, Title: title, Content: "x", ConflictChecked: true})
	}

	svc := NewContextService(memories, &fakeRelations{}, WithNormativeThreshold(2))
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

// TestContextHealthOpenConflicts verifica que un conflicto real juzgado (conflicts_with) se
// superficia en la bandeja "Salud de memoria" — el agujero que antes dejaba invisible al humano.
func TestContextHealthOpenConflicts(t *testing.T) {
	ctx := context.Background()
	memories := newFakeObs()
	memories.Add(ctx, domain.Memory{Project: "kandel", Type: domain.TypeDecision, Title: "A", Content: "x", ConflictChecked: true})
	rels := &fakeRelations{}
	rels.Insert(ctx, domain.MemoryRelation{SourceID: "mem-A", TargetID: "mem-B", Kind: domain.RelationConflictsWith, Status: domain.JudgmentJudged})

	svc := NewContextService(memories, rels)
	out, err := svc.Format(ctx, "kandel", domain.Scope(""))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if !strings.Contains(out, "conflicto(s) sin resolver") {
		t.Errorf("un conflicts_with juzgado debe avisar en Salud de memoria:\n%s", out)
	}
}
