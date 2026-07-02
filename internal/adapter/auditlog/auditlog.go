// Package auditlog escribe un registro de auditoría append-only (JSONL) de las acciones de
// curación de la TUI (borrar/restaurar). Es el rastro que permite revisar —y, si hace falta,
// deshacer— un borrado hecho por error, aunque se haya confirmado. Como toda eliminación es
// lógica, la recuperación real la da el restore; este log es la auditoría de qué pasó y cuándo.
package auditlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Logger vuelca una línea JSON por acción al archivo indicado. Es best-effort: un fallo de E/S no
// debe tumbar la interfaz, así que los errores se ignoran (la auditoría es secundaria a la acción).
type Logger struct {
	path string
}

// New construye un Logger que escribe en path. Crea el directorio contenedor si falta.
func New(path string) *Logger {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return &Logger{path: path}
}

// entry es la forma de cada línea del log. El orden de los campos fija el orden en el JSON.
type entry struct {
	TS     string `json:"ts"`
	Action string `json:"action"`
	ID     int64  `json:"id"`
	Title  string `json:"title"`
}

// LogAction añade una línea al log con la marca de tiempo UTC, la acción (p. ej. "soft_delete" o
// "restore"), el id y el título de la memoria afectada. Best-effort: si algo falla, no propaga.
func (l *Logger) LogAction(action string, id int64, title string) {
	b, err := json.Marshal(entry{
		TS:     time.Now().UTC().Format(time.RFC3339),
		Action: action,
		ID:     id,
		Title:  title,
	})
	if err != nil {
		return
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}
