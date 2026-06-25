package ports

import "context"

// Neighbor es un vecino devuelto por la búsqueda vectorial: el id de una observación y su
// similitud de coseno respecto a la consulta.
type Neighbor struct {
	ObservationID int64
	Score         float64 // similitud de coseno [0..1]; mayor = más cercano
}

// VectorIndex guarda los vectores de las observaciones y resuelve búsquedas por cercanía.
// La implementación default guarda el vector como BLOB en SQLite y calcula el coseno en Go
// puro (preserva CGO=0 / binario único). Como es un port, migrar a sqlite-vec más adelante es
// un cambio de adaptador, no una reescritura.
type VectorIndex interface {
	// Upsert guarda (o reemplaza) el vector de una observación, anotando el modelo que lo generó.
	Upsert(ctx context.Context, observationID int64, vector []float32, model string) error
	// Nearest devuelve los vecinos más cercanos a la consulta, ordenados por cercanía.
	Nearest(ctx context.Context, query []float32, limit int) ([]Neighbor, error)
	// Delete quita el vector de una observación (al hacer hard-delete de la observación).
	Delete(ctx context.Context, observationID int64) error
}
