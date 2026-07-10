package service

import (
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/domain"
)

func sampleResults() []domain.SearchResult {
	return []domain.SearchResult{
		{Memory: domain.Memory{ID: 1, Type: domain.TypeDecision, Title: "Alta", Content: "cuerpo completo alto"}, Score: 0.80},
		{Memory: domain.Memory{ID: 2, Type: domain.TypeDiscovery, Title: "Media", Content: "cuerpo medio"}, Score: 0.55},
		{Memory: domain.Memory{ID: 3, Type: domain.TypeBugfix, Title: "Baja", Content: "cuerpo bajo"}, Score: 0.20},
	}
}

func TestFormatRelevantFiltersByThreshold(t *testing.T) {
	out := FormatRelevant(sampleResults(), 0.5, nil)
	if !strings.Contains(out, "cuerpo completo alto") || !strings.Contains(out, "cuerpo medio") {
		t.Errorf("debía incluir las memorias sobre el umbral; out=%s", out)
	}
	if strings.Contains(out, "cuerpo bajo") {
		t.Errorf("no debía incluir la memoria bajo el umbral; out=%s", out)
	}
}

func TestFormatRelevantIncludesFullBody(t *testing.T) {
	long := strings.Repeat("x", 1000)
	res := []domain.SearchResult{{Memory: domain.Memory{ID: 1, Type: domain.TypeDecision, Title: "T", Content: long}, Score: 0.9}}
	out := FormatRelevant(res, 0.5, nil)
	if !strings.Contains(out, long) {
		t.Errorf("el cuerpo debe ir COMPLETO, sin recorte (a diferencia del arranque)")
	}
}

func TestFormatRelevantExcludesInjected(t *testing.T) {
	out := FormatRelevant(sampleResults(), 0.5, map[int64]bool{1: true})
	if strings.Contains(out, "cuerpo completo alto") {
		t.Errorf("la memoria ya inyectada (id 1) no debía repetirse; out=%s", out)
	}
	if !strings.Contains(out, "cuerpo medio") {
		t.Errorf("la memoria no excluida debía aparecer; out=%s", out)
	}
}

func TestFormatRelevantEmptyWhenNothingPasses(t *testing.T) {
	if out := FormatRelevant(sampleResults(), 0.99, nil); out != "" {
		t.Errorf("sin coincidencias sobre el umbral debe devolver \"\", dio: %q", out)
	}
	if out := FormatRelevant(nil, 0.5, nil); out != "" {
		t.Errorf("sin resultados debe devolver \"\", dio: %q", out)
	}
}

func TestRelevantIDsMatchesFilter(t *testing.T) {
	ids := RelevantIDs(sampleResults(), 0.5, map[int64]bool{1: true})
	if len(ids) != 1 || ids[0] != 2 {
		t.Errorf("RelevantIDs debe devolver solo los conservados (id 2), dio: %v", ids)
	}
}
