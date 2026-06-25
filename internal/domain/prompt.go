package domain

import "time"

// Prompt es lo que el usuario pidió, guardado como contexto crudo y recuperable. Se mantiene
// deliberadamente separado de Observation: un prompt es lo que se dijo sin curar; una
// observación es algo que el agente decidió que vale la pena recordar.
type Prompt struct {
	ID        int64
	SyncID    string
	SessionID string
	Project   string
	Content   string
	CreatedAt time.Time
}
