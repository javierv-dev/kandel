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

// argStringList lee un argumento de tipo array de texto; nil si falta o no es un array. Recorta cada
// elemento y descarta los vacíos.
func argStringList(req mcp.CallToolRequest, name string) []string {
	raw, ok := req.GetArguments()[name].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
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

// argFloat lee un argumento numérico como float64; def si falta.
func argFloat(req mcp.CallToolRequest, name string, def float64) float64 {
	switch v := req.GetArguments()[name].(type) {
	case float64:
		return v
	case int:
		return float64(v)
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

// jsonResult serializa un valor a JSON para devolverlo como texto de resultado. Si falla la
// serialización, devuelve el error en texto plano.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError("error al serializar el resultado: " + err.Error()), nil
	}
	return mcp.NewToolResultText(string(out)), nil
}
