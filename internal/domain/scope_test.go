package domain

import (
	"errors"
	"testing"
)

func TestParseScope(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Scope
		wantErr error
	}{
		{"vacío usa por defecto", "", ScopeProject, nil},
		{"espacios usan por defecto", "   ", ScopeProject, nil},
		{"project explícito", "project", ScopeProject, nil},
		{"personal", "personal", ScopePersonal, nil},
		{"global", "global", ScopeGlobal, nil},
		{"mayúsculas se normalizan", "PERSONAL", ScopePersonal, nil},
		{"con espacios alrededor", "  global  ", ScopeGlobal, nil},
		{"inválido", "equipo", "", ErrInvalidScope},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseScope(tc.raw)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, se esperaba %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("scope = %q, se esperaba %q", got, tc.want)
			}
		})
	}
}

func TestScopeValid(t *testing.T) {
	valid := []Scope{ScopeProject, ScopePersonal, ScopeGlobal}
	for _, s := range valid {
		if !s.Valid() {
			t.Errorf("%q debería ser válido", s)
		}
	}
	if Scope("otro").Valid() {
		t.Error("\"otro\" no debería ser válido")
	}
}
