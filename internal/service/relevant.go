package service

import (
	"fmt"
	"strings"

	"github.com/javierv-dev/kandel/internal/domain"
)

// FormatRelevant arma el bloque de "memoria relevante" que el hook UserPromptSubmit inyecta para un
// prompt concreto (Capa 2 del rediseño de contexto por relevancia). Toma los resultados de la
// búsqueda semántica —ya rankeados por el servicio— y:
//   - descarta los que no superan el umbral de similitud (threshold),
//   - descarta los ya inyectados antes en la sesión (exclude, dedup por sesión),
//   - formatea el resto con su CUERPO COMPLETO (sin recorte, a diferencia de la inyección de
//     arranque, que solo trae títulos).
//
// Devuelve "" si no queda nada que mostrar: sin coincidencia relevante, el hook no inyecta nada (no
// mete ruido). exclude puede ser nil (leer de un mapa nil es seguro en Go).
func FormatRelevant(results []domain.SearchResult, threshold float64, exclude map[int64]bool) string {
	var kept []domain.SearchResult
	for _, r := range results {
		if r.Score < threshold {
			continue
		}
		if exclude[r.Memory.ID] {
			continue
		}
		kept = append(kept, r)
	}
	if len(kept) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Memoria relevante para tu petición\n\n")
	b.WriteString("Recuperada por significado a partir de tu mensaje. Úsala si aplica; ignórala si no.\n\n")
	for _, r := range kept {
		fmt.Fprintf(&b, "### %s\n", r.Memory.Title)
		fmt.Fprintf(&b, "[%s] · id %d · relevancia %.2f\n\n", r.Memory.Type, r.Memory.ID, r.Score)
		b.WriteString(strings.TrimSpace(r.Memory.Content))
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// RelevantIDs devuelve los ids de las memorias que FormatRelevant habría conservado con el mismo
// umbral y exclusiones. Lo usa el hook para registrar qué se inyectó y no repetirlo en el siguiente
// turno (dedup por sesión).
func RelevantIDs(results []domain.SearchResult, threshold float64, exclude map[int64]bool) []int64 {
	var ids []int64
	for _, r := range results {
		if r.Score < threshold || exclude[r.Memory.ID] {
			continue
		}
		ids = append(ids, r.Memory.ID)
	}
	return ids
}
