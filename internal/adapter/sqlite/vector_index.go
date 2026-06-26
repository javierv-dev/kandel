package sqlite

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"github.com/javierv-dev/kandel/internal/ports"
)

// VectorIndex implementa ports.VectorIndex guardando cada vector como BLOB en SQLite y
// resolviendo la cercanía con coseno en Go puro. Así se preserva CGO=0 y el binario único:
// no hace falta sqlite-vec ni extensiones nativas. A escala local (miles de vectores) el
// escaneo lineal es de pocos milisegundos. Como es un port, migrar a sqlite-vec más adelante
// sería un cambio de adaptador, no una reescritura.
type VectorIndex struct {
	db execer
}

var _ ports.VectorIndex = (*VectorIndex)(nil)

// NewVectorIndex crea el índice vectorial sobre la conexión dada.
func NewVectorIndex(db *DB) *VectorIndex {
	return &VectorIndex{db: db.sql}
}

// Upsert guarda (o reemplaza) el vector de una memoria, anotando el modelo y la dimensión.
func (v *VectorIndex) Upsert(ctx context.Context, memoryID int64, vector []float32, model string) error {
	const q = `INSERT INTO memory_vectors (memory_id, model, dimensions, vector, created_at)
		VALUES (?, ?, ?, ?, datetime('now'))
		ON CONFLICT(memory_id) DO UPDATE SET
			model = excluded.model,
			dimensions = excluded.dimensions,
			vector = excluded.vector,
			created_at = excluded.created_at`
	if _, err := execFromCtx(ctx, v.db).ExecContext(ctx, q, memoryID, model, len(vector), encodeVector(vector)); err != nil {
		return fmt.Errorf("sqlite: upsert vector: %w", err)
	}
	return nil
}

// Nearest carga todos los vectores, calcula la similitud de coseno contra la consulta y devuelve
// los más cercanos, ordenados de mayor a menor. El filtrado por proyecto/alcance lo hace después
// MemoryRepo.Search sobre los vecinos devueltos.
func (v *VectorIndex) Nearest(ctx context.Context, query []float32, limit int) ([]ports.Neighbor, error) {
	rows, err := execFromCtx(ctx, v.db).QueryContext(ctx, `SELECT memory_id, vector FROM memory_vectors`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: leer vectores: %w", err)
	}
	defer rows.Close()

	var neighbors []ports.Neighbor
	for rows.Next() {
		var (
			id   int64
			blob []byte
		)
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		score := cosineSimilarity(query, decodeVector(blob))
		neighbors = append(neighbors, ports.Neighbor{MemoryID: id, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(neighbors, func(i, j int) bool { return neighbors[i].Score > neighbors[j].Score })
	if limit > 0 && len(neighbors) > limit {
		neighbors = neighbors[:limit]
	}
	return neighbors, nil
}

// Delete quita el vector de una memoria. (Con borrado físico de la memoria, la clave foránea
// ON DELETE CASCADE ya lo elimina; este método cubre el borrado explícito del vector.)
func (v *VectorIndex) Delete(ctx context.Context, memoryID int64) error {
	if _, err := execFromCtx(ctx, v.db).ExecContext(ctx, `DELETE FROM memory_vectors WHERE memory_id = ?`, memoryID); err != nil {
		return fmt.Errorf("sqlite: borrar vector: %w", err)
	}
	return nil
}

// encodeVector serializa un vector a bytes little-endian (4 bytes por float32).
func encodeVector(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, f := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeVector reconstruye un vector desde su BLOB little-endian.
func decodeVector(buf []byte) []float32 {
	vec := make([]float32, len(buf)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
	}
	return vec
}

// cosineSimilarity calcula la similitud de coseno entre dos vectores. Devuelve 0 si tienen
// distinta longitud o si alguno es el vector nulo (evita división por cero).
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		normA += x * x
		normB += y * y
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
