// Package chunkcodec canonicaliza y direcciona por contenido los chunks de sincronización. Un
// chunk es un lote de mutaciones serializado de forma determinista; su identidad (ChunkID) es el
// hash de su contenido, de modo que dos máquinas que produzcan el mismo lote obtienen el mismo id
// (idempotencia de envío e importación).
package chunkcodec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/javierv-dev/kandel/internal/domain"
)

// formatVersion identifica el formato del chunk; se persiste para poder evolucionarlo.
//
// Política de compatibilidad de sync (§21 Eje 4 del plan): Decode RECHAZA cualquier chunk cuya
// versión no sea exactamente formatVersion. Es decir, no hay compatibilidad hacia atrás ni hacia
// adelante entre formatos distintos: un binario solo importa chunks de su misma versión de formato.
// Por tanto, **subir formatVersion es un cambio incompatible y obliga a subir el MAJOR del producto
// (SemVer, §21 Eje 1)**. Leer versiones menores hacia atrás queda como mejora futura (no urge); si
// se implementa, relaja la comparación estricta de Decode y deja de forzar el bump de MAJOR.
const formatVersion = 1

// chunk es la forma serializada: una versión y la lista ordenada de mutaciones.
type chunk struct {
	Version   int                   `json:"version"`
	Mutations []domain.SyncMutation `json:"mutations"`
}

// Encode serializa un lote de mutaciones a bytes canónicos y devuelve también su ChunkID
// (content-addressed). El orden de las mutaciones se respeta tal cual: el llamador las entrega ya
// ordenadas por Seq, lo que hace la salida estable y reproducible.
func Encode(mutations []domain.SyncMutation) (data []byte, chunkID string, err error) {
	data, err = json.Marshal(chunk{Version: formatVersion, Mutations: mutations})
	if err != nil {
		return nil, "", fmt.Errorf("chunkcodec: serializar: %w", err)
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

// Decode reconstruye las mutaciones de un chunk. Valida la versión de formato.
func Decode(data []byte) ([]domain.SyncMutation, error) {
	var c chunk
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("chunkcodec: deserializar: %w", err)
	}
	if c.Version != formatVersion {
		return nil, fmt.Errorf("chunkcodec: versión de chunk %d no soportada (se esperaba %d)", c.Version, formatVersion)
	}
	return c.Mutations, nil
}

// VerifyID recalcula el ChunkID de los datos y lo compara con el esperado: detecta corrupción o
// manipulación antes de aplicar un chunk.
func VerifyID(data []byte, expectedID string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != expectedID {
		return fmt.Errorf("chunkcodec: id de chunk no coincide (esperado %s, calculado %s)", expectedID, got)
	}
	return nil
}
