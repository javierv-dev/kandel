package llm

import (
	"context"
	"os"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

// TestOpenCodeJudgeLive es la prueba de funcionalidad real del juez con OpenCode. Está desactivada
// por defecto para que `go test ./...` no dependa de la red ni de la CLI: se activa con
// KANDEL_LIVE_OPENCODE=1 y usa el agente/modelo por defecto del registro (opencode).
func TestOpenCodeJudgeLive(t *testing.T) {
	if os.Getenv("KANDEL_LIVE_OPENCODE") != "1" {
		t.Skip("define KANDEL_LIVE_OPENCODE=1 para probar el juez real con OpenCode")
	}
	t.Setenv("KANDEL_AGENT_CLI", "opencode")
	t.Setenv("KANDEL_DATA_DIR", t.TempDir())

	const prompt = `Eres un auditor de memoria semántica. Compara las dos memorias y clasifica su relación.

=== Memoria A ===
ID: mem-test-a
Título: El juez usa el modelo X
Contenido: Decisión: el juez de conflictos usará OpenCode con DeepSeek V4.1 Flash high.

=== Memoria B ===
ID: mem-test-b
Título: El juez usaba el modelo Y
Contenido: Decisión anterior: el juez de conflictos usaba Pi con gemini-4-hydrogen-xhigh.

Responde con un único objeto JSON en una línea y nada más:
{"relation":"<verbo>","confidence":<0.0-1.0>,"reasoning":"<≤200 caracteres>"}`

	v, err := FromEnv().Judge(context.Background(), prompt)
	if err != nil {
		t.Fatalf("el juez real falló: %v", err)
	}
	if !v.Relation.Valid() || v.Relation == domain.RelationPending {
		t.Fatalf("relación inválida: %q", v.Relation)
	}
	if v.Confidence <= 0 || v.Confidence > 1 {
		t.Fatalf("confianza fuera de rango: %v", v.Confidence)
	}
	if v.Model == "" {
		t.Fatalf("el veredicto no reporta modelo")
	}
	t.Logf("veredicto real: relation=%s confidence=%.2f model=%s reasoning=%q",
		v.Relation, v.Confidence, v.Model, v.Reasoning)
}
