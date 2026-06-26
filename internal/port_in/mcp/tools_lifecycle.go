package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerLifecycleTools registra las herramientas de ciclo de vida (pin/unpin/review).
func registerLifecycleTools(srv *server.MCPServer, svc Services, allowlist map[string]bool) {
	if shouldRegister("mem_pin", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_pin",
				mcp.WithDescription("Fija una memoria arriba en el contexto (estado local, no se sincroniza)."),
				mcp.WithTitleAnnotation("Fijar memoria"),
				mcp.WithNumber("id", mcp.Required(), mcp.Description("Id de la memoria")),
			),
			handlePin(svc, true),
		)
	}

	if shouldRegister("mem_unpin", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_unpin",
				mcp.WithDescription("Desfija una memoria."),
				mcp.WithTitleAnnotation("Desfijar memoria"),
				mcp.WithNumber("id", mcp.Required(), mcp.Description("Id de la memoria")),
			),
			handlePin(svc, false),
		)
	}

	if shouldRegister("mem_review", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_review",
				mcp.WithDescription("Lista las memorias cuya fecha de revisión ya venció (decaimiento), para revisarlas o actualizarlas."),
				mcp.WithTitleAnnotation("Revisar memorias"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithString("project", mcp.Description("Filtrar por proyecto; si se omite, el del proceso")),
				mcp.WithNumber("limit", mcp.Description("Máximo de memorias (def. 20)")),
			),
			handleReview(svc),
		)
	}
}

// handlePin traduce mem_pin / mem_unpin a Lifecycle.Pin/Unpin.
func handlePin(svc Services, pinned bool) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(argInt(req, "id", 0))
		if id <= 0 {
			return mcp.NewToolResultError("id es obligatorio"), nil
		}
		var err error
		if pinned {
			err = svc.Lifecycle.Pin(ctx, id)
		} else {
			err = svc.Lifecycle.Unpin(ctx, id)
		}
		if err != nil {
			return mcp.NewToolResultError("error al cambiar el estado de fijado: " + err.Error()), nil
		}
		return jsonResult(map[string]any{"id": id, "pinned": pinned})
	}
}

// handleReview traduce mem_review a Lifecycle.NeedingReview.
func handleReview(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		memories, err := svc.Lifecycle.NeedingReview(ctx, svc.resolveProject(argString(req, "project")), argInt(req, "limit", 20))
		if err != nil {
			return mcp.NewToolResultError("error al listar memorias a revisar: " + err.Error()), nil
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
