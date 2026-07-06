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
