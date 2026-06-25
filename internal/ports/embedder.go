package ports

import "context"

// Embedder convierte texto en un vector. Es la pieza que hace posible la búsqueda semántica
// del agente. El default es Ollama por HTTP (binario kandel sigue puro-Go, CGO=0). El mismo
// modelo debe usarse al guardar y al buscar: mezclar modelos invalida las distancias de coseno.
type Embedder interface {
	// Embed convierte texto en un vector.
	Embed(ctx context.Context, text string) ([]float32, error)
	// Dimensions indica el tamaño del vector (para validar consistencia en la base de datos).
	Dimensions() int
	// Model identifica el modelo (se persiste junto al vector: es parte del contrato de datos).
	Model() string
}
