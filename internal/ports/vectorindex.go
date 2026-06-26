package ports

import "context"

// Neighbor es un vecino devuelto por la búsqueda vectorial: el id de una memoria y su
// similitud de coseno respecto a la consulta.
type Neighbor struct {
	MemoryID int64
	Score    float64 // similitud de coseno [0..1]; mayor = más cercano
}

// VectorIndex guarda los vectores de las memorias y resuelve búsquedas por cercanía.
// La implementación default guarda el vector como BLOB en SQLite y calcula el coseno en Go
// puro (preserva CGO=0 / binario único). Como es un port, migrar a sqlite-vec más adelante es
// un cambio de adaptador, no una reescritura.
type VectorIndex interface {
	// Upsert guarda (o reemplaza) el vector de una memoria, anotando el modelo que lo generó.
	Upsert(ctx context.Context, memoryID int64, vector []float32, model string) error
	// Nearest devuelve los vecinos más cercanos a la consulta, ordenados por cercanía.
	Nearest(ctx context.Context, query []float32, limit int) ([]Neighbor, error)
	// Delete quita el vector de una memoria (al hacer hard-delete de la memoria).
	Delete(ctx context.Context, memoryID int64) error
	// PendingEmbedding devuelve los ids de memorias vivas que aún no tienen vector, o cuyo
	// vector lo generó un modelo distinto al indicado (el modelo es contrato de datos: mezclar
	// modelos invalida las distancias de coseno). Es la base de los comandos de mantenimiento
	// "embeddings backfill/reembed". Un limit <= 0 devuelve todos los pendientes.
	PendingEmbedding(ctx context.Context, model string, limit int) ([]int64, error)
}
