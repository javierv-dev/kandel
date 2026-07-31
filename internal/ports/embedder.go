package ports

import (
	"context"
	"errors"
)

// ErrUnembeddable indica que el backend aceptó la petición pero no pudo producir un vector válido
// para ESE texto en particular (p. ej. bge-m3 en Ollama devuelve NaN para ciertas secuencias de
// tokens y Ollama, que no serializa NaN a JSON, responde 500). Es un fallo POR-ENTRADA, no del
// backend: el embebedor sigue sano para otros textos. El backfill lo distingue de una caída (red o
// 500 genérico) para SALTAR la memoria envenenada y seguir, en vez de abortar toda la pasada.
var ErrUnembeddable = errors.New("embebedor: texto no vectorizable")

// Embedder convierte texto en un vector. Es la pieza que hace posible la búsqueda semántica
// del agente. El default es Ollama por HTTP (binario kandel sigue puro-Go, CGO=0). El mismo
// modelo debe usarse al guardar y al buscar: mezclar modelos invalida las distancias de coseno.
type Embedder interface {
	// Embed convierte texto en un vector.
	Embed(ctx context.Context, text string) ([]float32, error)
	// EmbedBatch convierte varios textos en sus vectores en una sola petición, preservando el
	// orden de entrada (el vector i corresponde al texto i). Permite embeber contenido y título de
	// una memoria en un único viaje de red. Con lista vacía devuelve una lista vacía sin llamar al
	// backend.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	// Dimensions indica el tamaño del vector (para validar consistencia en la base de datos).
	Dimensions() int
	// Model identifica el modelo (se persiste junto al vector: es parte del contrato de datos).
	Model() string
}
