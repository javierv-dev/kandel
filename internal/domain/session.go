package domain

import "time"

// Session agrupa el trabajo de una sesión del agente (proyecto, directorio y momento).
// No guarda conocimiento: es el contenedor temporal de las memorias.
type Session struct {
	ID             string
	Project        string
	Directory      string
	Application    string
	ConversationID string
	StartedAt      time.Time
	EndedAt        *time.Time // nil mientras la sesión sigue abierta
}

// IsOpen indica si la sesión aún no se ha cerrado.
func (s Session) IsOpen() bool {
	return s.EndedAt == nil
}
