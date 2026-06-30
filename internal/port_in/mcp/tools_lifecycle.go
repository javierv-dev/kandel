package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerLifecycleTools registra las herramientas de ciclo de vida (pendientes por resolver).
func registerLifecycleTools(srv *server.MCPServer, svc Services, allowlist map[string]bool) {
	if shouldRegister("mem_pending", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_pending",
				mcp.WithDescription("Lista las memorias pendientes por resolver (resolution=open)."),
				mcp.WithTitleAnnotation("Pendientes"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithString("project", mcp.Description("Filtrar por proyecto; si se omite, el del proceso")),
				mcp.WithNumber("limit", mcp.Description("Máximo de memorias (def. 20)")),
			),
			handlePending(svc),
		)
	}
}

// handlePending traduce mem_pending a Lifecycle.PendingResolution.
func handlePending(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		memories, err := svc.Lifecycle.PendingResolution(ctx, svc.resolveProject(argString(req, "project")), argInt(req, "limit", 20))
		if err != nil {
			return mcp.NewToolResultError("error al listar memorias pendientes: " + err.Error()), nil
		}
		items := make([]map[string]any, 0, len(memories))
		for _, m := range memories {
			items = append(items, map[string]any{
				"id":    m.ID,
				"type":  string(m.Type),
				"title": m.Title,
			})
		}
		return jsonResult(map[string]any{"memories": items})
	}
}
