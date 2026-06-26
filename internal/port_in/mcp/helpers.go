package mcp

import (
	"encoding/json"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// argString lee un argumento de texto de la petición; vacío si falta o no es texto.
func argString(req mcp.CallToolRequest, name string) string {
	v, _ := req.GetArguments()[name].(string)
	return strings.TrimSpace(v)
}

// argInt lee un argumento numérico (los números JSON llegan como float64); def si falta.
func argInt(req mcp.CallToolRequest, name string, def int) int {
	switch v := req.GetArguments()[name].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return def
	}
}

// argBool lee un argumento booleano; def si falta.
func argBool(req mcp.CallToolRequest, name string, def bool) bool {
	if v, ok := req.GetArguments()[name].(bool); ok {
		return v
	}
	return def
}

// resolveProject aplica el override de proceso cuando la llamada no trae proyecto explícito.
func (s Services) resolveProject(explicit string) string {
	if p := strings.TrimSpace(explicit); p != "" {
		return p
	}
	return strings.TrimSpace(s.DefaultProject)
}

// jsonResult serializa un valor a JSON para devolverlo como texto de resultado. Si falla la
// serialización, devuelve el error en texto plano.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError("error al serializar el resultado: " + err.Error()), nil
	}
	return mcp.NewToolResultText(string(out)), nil
}
