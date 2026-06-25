package domain

import (
	"errors"
	"testing"
)

func TestParseTopicKey(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"un nivel", "config", true},
		{"dos niveles", "architecture/auth-model", true},
		{"con dígitos y guiones", "bug/nil-panic-user-list", true},
		{"con espacios alrededor", "  decision/db-choice  ", true},
		{"vacío inválido", "", false},
		{"solo espacios inválido", "   ", false},
		{"tres niveles inválido", "a/b/c", false},
		{"mayúsculas inválido", "Architecture/Auth", false},
		{"guion al inicio inválido", "-bug/panic", false},
		{"guion al final inválido", "bug-/panic", false},
		{"guion bajo inválido", "bug/nil_panic", false},
		{"segmento vacío inválido", "bug//panic", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTopicKey(tc.raw)
			if tc.ok {
				if err != nil {
					t.Fatalf("error inesperado: %v", err)
				}
				if got.IsZero() {
					t.Fatal("la clave no debería ser cero")
				}
			} else {
				if !errors.Is(err, ErrInvalidTopicKey) {
					t.Fatalf("se esperaba ErrInvalidTopicKey, se obtuvo %v", err)
				}
			}
		})
	}
}

func TestTopicKeyIsZero(t *testing.T) {
	if !TopicKey("").IsZero() {
		t.Error("la clave vacía debería ser cero")
	}
	if TopicKey("config").IsZero() {
		t.Error("una clave con valor no debería ser cero")
	}
}
