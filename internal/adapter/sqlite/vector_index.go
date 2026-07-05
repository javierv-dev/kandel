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
// Ambos vectores (contenido y título) viven en la MISMA fila de memory_vectors, en columnas
// distintas: comparten memory_id, model y dimensions. El índice del CONTENIDO es dueño de la fila
// (la crea/borra); el del TÍTULO solo escribe su columna sobre la fila existente.
type VectorIndex struct {
	db     execer
	column string // columna de este vector: vector_content o vector_title
	owner  bool   // true = índice del contenido (dueño de la fila); false = título (columna sobre la fila)
}

var _ ports.VectorIndex = (*VectorIndex)(nil)

// NewVectorIndex crea el índice vectorial del CONTENIDO (columna vector_content, dueño de la fila).
func NewVectorIndex(db *DB) *VectorIndex {
	return &VectorIndex{db: db.sql, column: "vector_content", owner: true}
}

// NewTitleVectorIndex crea el índice vectorial del TÍTULO (columna vector_title sobre la misma fila
// que el contenido). Sostiene el portón por título de la detección de casi-duplicados.
func NewTitleVectorIndex(db *DB) *VectorIndex {
	return &VectorIndex{db: db.sql, column: "vector_title", owner: false}
}

// Upsert guarda (o reemplaza) el vector de una memoria. El índice del contenido crea o actualiza la
// fila (con model/dimensions); el del título solo actualiza su columna sobre la fila existente (si la
// memoria aún no tiene fila —contenido diferido por embebedor caído— es un no-op y el backfill la
// rellenará). Las columnas son constantes del código (no input del usuario): interpolarlas es seguro.
func (v *VectorIndex) Upsert(ctx context.Context, memoryID int64, vector []float32, model string) error {
	var q string
	var args []any
	if v.owner {
		q = fmt.Sprintf(`INSERT INTO memory_vectors (memory_id, model, dimensions, %[1]s, created_at)
			VALUES (?, ?, ?, ?, datetime('now'))
			ON CONFLICT(memory_id) DO UPDATE SET
				model = excluded.model,
				dimensions = excluded.dimensions,
				%[1]s = excluded.%[1]s,
				created_at = excluded.created_at`, v.column)
		args = []any{memoryID, model, len(vector), encodeVector(vector)}
	} else {
		q = fmt.Sprintf(`UPDATE memory_vectors SET %[1]s = ? WHERE memory_id = ?`, v.column)
		args = []any{encodeVector(vector), memoryID}
	}
	if _, err := execFromCtx(ctx, v.db).ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("sqlite: upsert vector: %w", err)
	}
	return nil
}

// Nearest carga los vectores de esta columna (los NULL se saltan), calcula la similitud de coseno
// contra la consulta y devuelve los más cercanos, ordenados de mayor a menor. El filtrado por
// proyecto/alcance lo hace después MemoryRepo.Search sobre los vecinos devueltos.
func (v *VectorIndex) Nearest(ctx context.Context, query []float32, limit int) ([]ports.Neighbor, error) {
	q := fmt.Sprintf(`SELECT memory_id, %[1]s FROM memory_vectors WHERE %[1]s IS NOT NULL`, v.column)
	rows, err := execFromCtx(ctx, v.db).QueryContext(ctx, q)
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

// Delete quita el vector de una memoria. El índice del contenido borra la fila entera (el título va
// con ella); el del título solo pone su columna a NULL. (Con hard-delete de la memoria, ON DELETE
// CASCADE ya elimina la fila; esto cubre el borrado explícito del vector.)
func (v *VectorIndex) Delete(ctx context.Context, memoryID int64) error {
	var q string
	if v.owner {
		q = `DELETE FROM memory_vectors WHERE memory_id = ?`
	} else {
		q = fmt.Sprintf(`UPDATE memory_vectors SET %[1]s = NULL WHERE memory_id = ?`, v.column)
	}
	if _, err := execFromCtx(ctx, v.db).ExecContext(ctx, q, memoryID); err != nil {
		return fmt.Errorf("sqlite: borrar vector: %w", err)
	}
	return nil
}

// PendingEmbedding lista los ids de memorias vivas cuyo vector de esta columna falta (o, para el
// contenido, es de otro modelo), ordenados por id ascendente para que el backfill por lotes avance
// de forma estable. Para el CONTENIDO: memorias sin fila o con modelo distinto. Para el TÍTULO:
// memorias que YA tienen fila de contenido (del modelo actual) pero con vector_title NULL — el hueco
// típico tras la migración, cuando el contenido ya estaba embebido y el título aún no.
func (v *VectorIndex) PendingEmbedding(ctx context.Context, model string, limit int) ([]int64, error) {
	var query string
	if v.owner {
		query = `SELECT m.id FROM memories m
			LEFT JOIN memory_vectors mv ON mv.memory_id = m.id
			WHERE m.deleted_at IS NULL AND (mv.memory_id IS NULL OR mv.model != ?)
			ORDER BY m.id ASC`
	} else {
		query = `SELECT m.id FROM memories m
			JOIN memory_vectors mv ON mv.memory_id = m.id
			WHERE m.deleted_at IS NULL AND mv.model = ? AND mv.vector_title IS NULL
			ORDER BY m.id ASC`
	}
	args := []any{model}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := execFromCtx(ctx, v.db).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: listar memorias pendientes de vector: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
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
