package domain

import "time"

// Session agrupa el trabajo de una sesión del agente (proyecto, directorio y momento).
// No guarda conocimiento: es el contenedor temporal de las observaciones.
type Session struct {
	ID        string
	Project   string
	Directory string
	StartedAt time.Time
	EndedAt   *time.Time // nil mientras la sesión sigue abierta
	Summary   string     // resumen al cerrar; vacío si no hay
}

// IsOpen indica si la sesión aún no se ha cerrado.
func (s Session) IsOpen() bool {
	return s.EndedAt == nil
}
