package config

import (
	"encoding/base64"
	"testing"
)

func TestGenerateToken(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// Debe decodificar como base64url sin relleno y rendir exactamente tokenBytes de entropía.
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("el token no es base64url válido: %v", err)
	}
	if len(raw) != tokenBytes {
		t.Errorf("se esperaban %d bytes de entropía, fueron %d", tokenBytes, len(raw))
	}

	// Dos llamadas no deben coincidir (aleatoriedad real, no un valor fijo).
	other, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken (2): %v", err)
	}
	if token == other {
		t.Error("dos tokens generados no deben coincidir")
	}
}
