package domain

import "time"

// Prompt es lo que el usuario pidió, guardado como contexto crudo y recuperable. Se mantiene
// deliberadamente separado de Memory: un prompt es lo que se dijo sin curar; una
// memoria es algo que el agente decidió que vale la pena recordar.
type Prompt struct {
	ID        int64
	SyncID    string
	SessionID string
	Project   string
	Content   string
	CreatedAt time.Time
}
