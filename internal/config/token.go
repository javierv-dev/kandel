package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// tokenBytes es la longitud en bytes del token de sync generado: 32 bytes = 256 bits de entropía,
// suficiente para un secreto tipo capability inadivinable por fuerza bruta.
const tokenBytes = 32

// GenerateToken produce un token de alta entropía (256 bits) codificado en base64url sin relleno,
// apto para usarse como sync_token. Usa crypto/rand: si la fuente de aleatoriedad del sistema falla,
// devuelve el error en vez de emitir un token débil. El token es a la vez el namespace del usuario en
// el hub y su secreto de acceso; por eso debe ser aleatorio, no escrito a mano.
func GenerateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generar token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
