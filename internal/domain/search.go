package domain

// SearchResult es una observación recuperada por la búsqueda semántica junto con su
// puntuación de cercanía. La puntuación es la similitud de coseno [0..1] respecto a la
// consulta: cuanto más alta, más relevante.
type SearchResult struct {
	Observation
	Score float64
}
