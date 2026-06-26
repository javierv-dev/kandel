package mcp

import (
	"context"

	"github.com/javierv-dev/kandel/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerSessionTools registra las herramientas de sesión.
func registerSessionTools(srv *server.MCPServer, svc Services, allowlist map[string]bool) {
	if shouldRegister("mem_session_start", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_session_start",
				mcp.WithDescription("Registra el inicio de una sesión de trabajo."),
				mcp.WithTitleAnnotation("Iniciar sesión"),
				mcp.WithString("id", mcp.Required(), mcp.Description("Id de la sesión")),
				mcp.WithString("project", mcp.Description("Proyecto; si se omite, el del proceso")),
				mcp.WithString("directory", mcp.Description("Directorio de trabajo")),
			),
			handleSessionStart(svc),
		)
	}

	if shouldRegister("mem_session_end", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_session_end",
				mcp.WithDescription("Marca una sesión como terminada, con un resumen opcional."),
				mcp.WithTitleAnnotation("Terminar sesión"),
				mcp.WithString("id", mcp.Required(), mcp.Description("Id de la sesión")),
				mcp.WithString("summary", mcp.Description("Resumen de la sesión")),
			),
			handleSessionEnd(svc, false),
		)
	}

	if shouldRegister("mem_session_summary", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_session_summary",
				mcp.WithDescription("Guarda el resumen de fin de sesión (cierra la sesión con el resumen). Llámala antes de dar el trabajo por terminado."),
				mcp.WithTitleAnnotation("Resumen de sesión"),
				mcp.WithString("id", mcp.Required(), mcp.Description("Id de la sesión")),
				mcp.WithString("summary", mcp.Required(), mcp.Description("Resumen de lo hecho en la sesión")),
			),
			handleSessionEnd(svc, true),
		)
	}
}

// handleSessionStart traduce la llamada mem_session_start a Session.Start.
func handleSessionStart(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := argString(req, "id")
		if id == "" {
			return mcp.NewToolResultError("id es obligatorio para mem_session_start"), nil
		}
		s, err := svc.Session.Start(ctx, service.StartInput{
			ID:        id,
			Project:   svc.resolveProject(argString(req, "project")),
			Directory: argString(req, "directory"),
		})
		if err != nil {
			return mcp.NewToolResultError("error al iniciar la sesión: " + err.Error()), nil
		}
		return jsonResult(map[string]any{"id": s.ID, "project": s.Project})
	}
}

// handleSessionEnd traduce mem_session_end / mem_session_summary a Session.End. Cuando
// requireSummary es true (mem_session_summary), el resumen es obligatorio.
func handleSessionEnd(svc Services, requireSummary bool) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := argString(req, "id")
		if id == "" {
			return mcp.NewToolResultError("id es obligatorio"), nil
		}
		summary := argString(req, "summary")
		if requireSummary && summary == "" {
			return mcp.NewToolResultError("summary es obligatorio para mem_session_summary"), nil
		}
		if err := svc.Session.End(ctx, id, summary); err != nil {
			return mcp.NewToolResultError("error al cerrar la sesión: " + err.Error()), nil
		}
		return jsonResult(map[string]any{"id": id, "ended": true})
	}
}
