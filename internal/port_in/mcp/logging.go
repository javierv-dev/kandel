package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// loggingMiddleware envuelve cada handler de tool para registrar la llamada y su resultado en
// ~/.kandel/logs/mcp.log. Da observabilidad al camino MCP (igual que los hooks tienen hooks.log):
// permite ver si el agente llama de verdad a mem_save y demás, y si la llamada falló (p. ej. con el
// embebedor caído, mem_save revierte y NO guarda nada). No altera el resultado devuelto al agente.
func loggingMiddleware(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := next(ctx, req)
		mcplog(req.Params.Name, res, err)
		return res, err
	}
}

// mcplog anexa una línea con el nombre de la tool y su desenlace (ok / error) al log de MCP.
// Nunca rompe la llamada: cualquier fallo al escribir el log se ignora.
func mcplog(tool string, res *mcp.CallToolResult, err error) {
	dir := mcpLogDir()
	if dir == "" {
		return
	}
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return
	}
	f, openErr := os.OpenFile(filepath.Join(dir, "mcp.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if openErr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s [mcp] %s -> %s\n", time.Now().Format("2006-01-02 15:04:05"), tool, outcome(res, err))
}

// outcome resume el desenlace de una llamada para el log: un error de transporte, un error de tool
// (con su mensaje) o "ok".
func outcome(res *mcp.CallToolResult, err error) string {
	if err != nil {
		return "ERROR(go): " + err.Error()
	}
	if res != nil && res.IsError {
		return "ERROR(tool): " + firstText(res)
	}
	return "ok"
}

// firstText extrae el primer fragmento de texto del resultado (el mensaje de error de la tool).
func firstText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(mcp.TextContent); ok {
			return strings.TrimSpace(t.Text)
		}
	}
	return "(sin detalle)"
}

// mcpLogDir resuelve el directorio de logs (KANDEL_LOG_DIR o ~/.kandel/logs). Mismo lugar que los
// hooks, para tener toda la observabilidad junta.
func mcpLogDir() string {
	if d := strings.TrimSpace(os.Getenv("KANDEL_LOG_DIR")); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kandel", "logs")
}
