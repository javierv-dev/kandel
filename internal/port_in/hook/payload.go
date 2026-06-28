package hook

import (
	"encoding/json"
	"io"
)

// payload son los campos del JSON que los agentes envían a un hook por stdin. No todos los hooks
// usan todos los campos (p. ej. prompt solo en UserPromptSubmit, stdout solo en SubagentStop).
type payload struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	Prompt    string `json:"prompt"`
	Stdout    string `json:"stdout"`
}

// parsePayload lee y decodifica el JSON de entrada. Es tolerante a propósito: si el cuerpo está
// vacío o malformado devuelve un payload con los campos en blanco (los hooks deciden qué hacer
// ante campos vacíos) en lugar de fallar, porque un hook nunca debe romper el flujo del agente.
func parsePayload(r io.Reader) payload {
	var p payload
	data, err := io.ReadAll(r)
	if err != nil || len(data) == 0 {
		return p
	}
	_ = json.Unmarshal(data, &p)
	return p
}
