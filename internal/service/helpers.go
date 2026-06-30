// Package service contiene los casos de uso: orquesta los ports (repos, sync, reloj) y aloja la
// lógica de aplicación (dedupe, fusión por tema, decaimiento, resolución de proyecto). No conoce
// SQL, HTTP ni MCP; depende solo de domain y ports.
package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// defaultSyncTarget es el destino de sincronización por defecto al encolar mutaciones.
const defaultSyncTarget = "remote"

// sourceLocal identifica que la mutación se originó en esta máquina.
const sourceLocal = "local"

// normalizeProject aplica la normalización canónica del nombre de proyecto: minúsculas, sin
// espacios sobrantes y guiones/guiones bajos consecutivos colapsados. Una cadena vacía se
// mantiene vacía (sin proyecto).
func normalizeProject(project string) string {
	n := strings.TrimSpace(strings.ToLower(project))
	for strings.Contains(n, "--") {
		n = strings.ReplaceAll(n, "--", "-")
	}
	for strings.Contains(n, "__") {
		n = strings.ReplaceAll(n, "__", "_")
	}
	return n
}

// hashContent calcula la huella del contenido normalizado (minúsculas, espacios colapsados a uno).
// Detecta duplicados exactos sin importar variaciones triviales de formato.
func hashContent(content string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(content), " "))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// newSyncID genera un identificador estable de sincronización con el prefijo dado. Si el lector
// de aleatoriedad falla, cae a un valor basado en el tiempo (suficientemente único).
func newSyncID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return prefix + "-" + hex.EncodeToString([]byte(time.Now().UTC().String()))
	}
	return prefix + "-" + hex.EncodeToString(b)
}
