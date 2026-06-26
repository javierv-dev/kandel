package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerDoctorTools registra la herramienta de diagnóstico de solo lectura.
func registerDoctorTools(srv *server.MCPServer, svc Services, allowlist map[string]bool) {
	if svc.Doctor == nil || !shouldRegister("mem_doctor", allowlist) {
		return
	}
	srv.AddTool(
		mcp.NewTool("mem_doctor",
			mcp.WithDescription("Ejecuta chequeos de salud de solo lectura del sistema de memoria (embebedor accesible, memorias sin vector, etc.) y devuelve un reporte. No modifica nada."),
			mcp.WithTitleAnnotation("Diagnóstico"),
			mcp.WithReadOnlyHintAnnotation(true),
		),
		handleDoctor(svc),
	)
}

// handleDoctor corre los chequeos y devuelve el reporte como JSON.
func handleDoctor(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		report := svc.Doctor.Run(ctx)
		return jsonResult(map[string]any{
			"ok":       report.OK(),
			"worst":    string(report.Worst),
			"findings": report.Findings,
		})
	}
}
