package service

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// seedToolName marca la procedencia de las memorias creadas por el seed, para poder distinguirlas
// (y, si hace falta, limpiarlas) más tarde.
const seedToolName = "kandel-seed"

// seedPrompt es el prompt canónico del seed, embebido desde seed_prompt.txt. Es la ÚNICA fuente de
// verdad de "qué es una decisión digna de memoria"; la skill /kandel-seed describe la misma tarea.
//
//go:embed seed_prompt.txt
var seedPrompt string

// allowedSeedTypes restringe los tipos que el seed acepta a las decisiones de alto valor (alcance de
// la Fase 12, §19 del plan). Un candidato con otro tipo se descarta.
var allowedSeedTypes = map[domain.MemoryType]bool{
	domain.TypeDecision:     true,
	domain.TypeArchitecture: true,
	domain.TypePreference:   true,
}

// SeedCandidate es una decisión propuesta por el LLM, antes de guardarse como memoria.
type SeedCandidate struct {
	Type    string `json:"type"`
	Scope   string `json:"scope"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// SeedGenerateInput son los datos para generar candidatos: la documentación ya reunida y el agente/
// modelo a usar.
type SeedGenerateInput struct {
	Sources  string // documentación del proyecto reunida (ver internal/seedsource)
	AgentCLI string // CLI de agente a invocar (claude, gemini, …); vacío = sin agente
	Model    string // modelo a usar; vacío = el por defecto de la CLI
	Limit    int    // máximo de candidatos; <=0 usa defaultSeedLimit
}

// SeedSaveResult resume qué pasó al guardar los candidatos.
type SeedSaveResult struct {
	Created   int
	Merged    int
	Duplicate int
	Failed    int
	Errors    []string
}

// memorySaver es la cara mínima de MemoryService que el seed necesita para guardar (inversión de
// dependencia para testear con un doble).
type memorySaver interface {
	Save(ctx context.Context, in SaveInput) (SaveResult, error)
}

// SeedService implementa el caso de uso de poblar la memoria de un proyecto existente. Separa la
// generación (cara, llama al LLM) del guardado (gate de aprobación entre medias, en el adaptador
// CLI). Reusa MemoryService.Save: dedupe, embedding (tolerante) y sync vienen gratis.
type SeedService struct {
	memories     memorySaver
	newGenerator func(agentCLI, model string) ports.AgentGenerator
}

// NewSeedService construye el servicio. newGenerator es una fábrica (inyectada desde main) que, dado
// el nombre de la CLI de agente y el modelo, devuelve el AgentGenerator concreto; así el service no
// conoce el adaptador llm y los tests inyectan un generador falso.
func NewSeedService(memories memorySaver, newGenerator func(agentCLI, model string) ports.AgentGenerator) *SeedService {
	return &SeedService{memories: memories, newGenerator: newGenerator}
}

// Generate pide al LLM que extraiga decisiones de alto valor de la documentación y devuelve los
// candidatos ya validados (tipos permitidos, título y contenido no vacíos). No escribe nada: el
// llamador muestra los candidatos y, tras aprobación, llama a Save.
//
// El límite (in.Limit) es un TOPE OPCIONAL, no un objetivo: in.Limit <= 0 significa SIN tope, y el
// LLM extrae cuantas decisiones reales encuentre (lo correcto para repos densos, donde un número
// fijo recortaría arbitrariamente). in.Limit > 0 acota tanto la instrucción al modelo como el
// recorte final. La calidad la garantiza el prompt (sesgo a decisiones, "no inventes") y el gate de
// aprobación del llamador, no este número.
func (s *SeedService) Generate(ctx context.Context, in SeedGenerateInput) ([]SeedCandidate, error) {
	if strings.TrimSpace(in.Sources) == "" {
		return nil, fmt.Errorf("seed: no se encontró documentación del proyecto para analizar")
	}

	// Instrucción de cantidad: tope explícito o "todas las que existan" si no hay tope.
	countLine := "Incluye TODAS las decisiones reales de alto valor que encuentres (sin inventar)."
	if in.Limit > 0 {
		countLine = fmt.Sprintf("MÁXIMO DE ELEMENTOS: %d", in.Limit)
	}

	gen := s.newGenerator(in.AgentCLI, in.Model)
	prompt := fmt.Sprintf("%s\n%s\n\n===== DOCUMENTACIÓN DEL PROYECTO =====\n%s",
		seedPrompt, countLine, in.Sources)

	out, err := gen.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("seed: el agente no generó candidatos: %w", err)
	}

	candidates, err := parseSeedCandidates(out)
	if err != nil {
		return nil, err
	}
	if in.Limit > 0 && len(candidates) > in.Limit {
		candidates = candidates[:in.Limit]
	}
	return candidates, nil
}

// Save persiste los candidatos como memorias (tipo de alto valor, procedencia "kandel-seed"). Ya no
// aborta si el embebedor está caído: MemoryService.Save es TOLERANTE y guarda cada memoria igual (sin
// vector), que el backfill posterior reembeberá. El dedupe de Save hace que re-ejecutar el seed sea
// idempotente. Devuelve el conteo por desenlace.
func (s *SeedService) Save(ctx context.Context, candidates []SeedCandidate, project string) (SeedSaveResult, error) {
	if len(candidates) == 0 {
		return SeedSaveResult{}, nil
	}

	var res SeedSaveResult
	for _, c := range candidates {
		scope, err := domain.ParseScope(c.Scope)
		if err != nil {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("%q: alcance inválido: %v", c.Title, err))
			continue
		}
		out, err := s.memories.Save(ctx, SaveInput{
			Project:  project,
			Type:     domain.MemoryType(c.Type),
			Scope:    scope,
			Title:    c.Title,
			Content:  c.Content,
			ToolName: seedToolName,
		})
		if err != nil {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("%q: %v", c.Title, err))
			continue
		}
		switch out.Outcome {
		case SaveCreated:
			res.Created++
		case SaveDuplicate:
			res.Duplicate++
		}
	}
	return res, nil
}

// parseSeedCandidates decodifica la salida del LLM en candidatos válidos. Es tolerante: quita vallas
// de código markdown y, si hay prosa alrededor, recorta al primer '[' … último ']'. Descarta los
// candidatos con tipo no permitido o sin título/contenido.
func parseSeedCandidates(raw string) ([]SeedCandidate, error) {
	text := strings.TrimSpace(raw)
	text = stripCodeFence(text)

	// Si hay texto alrededor del array, quedarse con el array JSON externo.
	if !strings.HasPrefix(text, "[") {
		start := strings.Index(text, "[")
		end := strings.LastIndex(text, "]")
		if start >= 0 && end > start {
			text = text[start : end+1]
		}
	}

	var parsed []SeedCandidate
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, fmt.Errorf("seed: la salida del agente no es un array JSON de candidatos: %w", err)
	}

	out := make([]SeedCandidate, 0, len(parsed))
	for _, c := range parsed {
		c.Type = strings.TrimSpace(strings.ToLower(c.Type))
		c.Scope = strings.TrimSpace(strings.ToLower(c.Scope))
		c.Title = strings.TrimSpace(c.Title)
		c.Content = strings.TrimSpace(c.Content)
		if c.Title == "" || c.Content == "" {
			continue
		}
		if !allowedSeedTypes[domain.MemoryType(c.Type)] {
			continue
		}
		if c.Scope == "" {
			c.Scope = string(domain.ScopeProject)
		}
		out = append(out, c)
	}
	return out, nil
}

// stripCodeFence quita una valla de código markdown que envuelva todo el texto (```json … ``` o
// ``` … ```). Si no hay valla, devuelve el texto tal cual.
func stripCodeFence(text string) string {
	if !strings.HasPrefix(text, "```") {
		return text
	}
	// Quitar la primera línea (```... ) y la última valla.
	if nl := strings.IndexByte(text, '\n'); nl >= 0 {
		text = text[nl+1:]
	}
	if end := strings.LastIndex(text, "```"); end >= 0 {
		text = text[:end]
	}
	return strings.TrimSpace(text)
}
