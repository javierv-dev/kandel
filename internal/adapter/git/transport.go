// Package git implementa ports.Transport guardando los chunks de sincronización como archivos en
// un directorio (por defecto .kandel/ dentro del repositorio). El nombre "git" alude a su uso: ese
// directorio se versiona con git y el usuario lo comparte con commit/push; kandel solo lee y
// escribe los archivos. La misma orquestación de sync sirve mañana sobre un servidor remoto sin
// tocar el servicio: solo cambia esta implementación de Transport.
package git

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/javierv-dev/kandel/internal/ports"
)

// manifestFile es el nombre del índice de chunks dentro del directorio.
const manifestFile = "manifest.json"

// chunksDir es la subcarpeta donde viven los archivos de chunk.
const chunksDir = "chunks"

// Transport guarda manifiesto y chunks en un directorio del sistema de archivos.
type Transport struct {
	root string
}

var _ ports.Transport = (*Transport)(nil)

// New crea el transporte enraizado en root (p. ej. ".kandel"). El directorio y su subcarpeta de
// chunks se crean al escribir; no es error que aún no existan al leer.
func New(root string) *Transport {
	return &Transport{root: root}
}

// ReadManifest lee el manifiesto; devuelve uno vacío (Version=1) si aún no existe.
func (t *Transport) ReadManifest() (*ports.Manifest, error) {
	data, err := os.ReadFile(filepath.Join(t.root, manifestFile))
	if os.IsNotExist(err) {
		return &ports.Manifest{Version: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("git: leer manifiesto: %w", err)
	}
	var m ports.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("git: parsear manifiesto: %w", err)
	}
	return &m, nil
}

// WriteManifest persiste el manifiesto, creando el directorio si hace falta.
func (t *Transport) WriteManifest(m *ports.Manifest) error {
	if err := os.MkdirAll(t.root, 0o755); err != nil {
		return fmt.Errorf("git: crear directorio: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("git: serializar manifiesto: %w", err)
	}
	if err := os.WriteFile(filepath.Join(t.root, manifestFile), data, 0o644); err != nil {
		return fmt.Errorf("git: escribir manifiesto: %w", err)
	}
	return nil
}

// WriteChunk escribe los bytes de un chunk en chunks/<id>.json. El entry no se usa aquí (sus
// metadatos viven en el manifiesto), pero forma parte del contrato del port.
func (t *Transport) WriteChunk(chunkID string, data []byte, _ ports.ChunkEntry) error {
	dir := filepath.Join(t.root, chunksDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("git: crear carpeta de chunks: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, chunkID+".json"), data, 0o644); err != nil {
		return fmt.Errorf("git: escribir chunk %s: %w", chunkID, err)
	}
	return nil
}

// ReadChunk lee los bytes de un chunk por su id.
func (t *Transport) ReadChunk(chunkID string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(t.root, chunksDir, chunkID+".json"))
	if err != nil {
		return nil, fmt.Errorf("git: leer chunk %s: %w", chunkID, err)
	}
	return data, nil
}
