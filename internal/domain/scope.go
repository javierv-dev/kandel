package domain

import "strings"

// Scope indica el alcance de privacidad de una memoria. Vive pegado al dato (no en una
// configuración aparte) porque decide qué se comparte al sincronizar.
type Scope string

const (
	// ScopeProject es el valor por defecto: memoria del proyecto, candidata a compartirse.
	ScopeProject Scope = "project"
	// ScopePersonal es memoria privada que no se replica.
	ScopePersonal Scope = "personal"
	// ScopeGlobal es memoria del usuario que cruza todos los proyectos.
	ScopeGlobal Scope = "global"
)

// Valid indica si el alcance es uno de los reconocidos.
func (s Scope) Valid() bool {
	switch s {
	case ScopeProject, ScopePersonal, ScopeGlobal:
		return true
	default:
		return false
	}
}

// ParseScope normaliza una cadena a un Scope. Una cadena vacía se interpreta como el valor
// por defecto (ScopeProject). Devuelve ErrInvalidScope si el valor no es reconocido.
func ParseScope(raw string) (Scope, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return ScopeProject, nil
	}
	scope := Scope(trimmed)
	if !scope.Valid() {
		return "", ErrInvalidScope
	}
	return scope, nil
}
