package domain

// SearchResult es una memoria recuperada por la búsqueda semántica junto con su
// puntuación de cercanía. La puntuación es la similitud de coseno [0..1] respecto a la
// consulta: cuanto más alta, más relevante.
type SearchResult struct {
	Memory
	Score float64
}
