package ports

// ChunkEntry son los metadatos de un chunk en el manifiesto: lo justo para localizarlo y
// validar su integridad sin abrirlo.
type ChunkEntry struct {
	ChunkID string // identificador content-addressed (hex)
	Count   int    // número de mutaciones que contiene
	Size    int64  // tamaño en bytes del chunk almacenado
}

// Manifest es el índice de chunks de un destino de sync: qué chunks existen y su versión de
// formato. Es el mapa que cada lado consulta para saber qué le falta.
type Manifest struct {
	Version int
	Chunks  []ChunkEntry
}

// Transport abstrae cómo se leen y escriben los chunks durante la sincronización. La misma
// orquestación de sync funciona sobre el sistema de archivos local (.kandel/) o sobre un
// servidor remoto sin cambiar la lógica: solo cambia la implementación de Transport.
type Transport interface {
	// ReadManifest devuelve el manifiesto; uno vacío (Version=1) si aún no existe.
	ReadManifest() (*Manifest, error)
	// WriteManifest persiste el manifiesto.
	WriteManifest(m *Manifest) error
	// WriteChunk escribe los bytes de un chunk identificado por chunkID, con sus metadatos.
	WriteChunk(chunkID string, data []byte, entry ChunkEntry) error
	// ReadChunk lee los bytes de un chunk por su id.
	ReadChunk(chunkID string) ([]byte, error)
}
