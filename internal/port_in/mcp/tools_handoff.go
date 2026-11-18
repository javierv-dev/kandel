package mcp

import (
	"context"

	"github.com/javierv-dev/kandel/internal/service"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func saveHandoff(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h, err := svc.Handoff.Save(ctx, service.HandoffInput{SessionID: argString(r, "session_id"), ParentID: argString(r, "parent_id"), RequestID: argString(r, "request_id"), Title: argString(r, "title"), Content: argString(r, "content")})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]string{"id": h.ID, "task_id": h.TaskID})
	}
}

func registerHandoffTools(srv *server.MCPServer, svc Services, allowlist map[string]bool) {
	if svc.Handoff == nil {
		return
	}
	session := mcp.WithString("session_id", mcp.Required(), mcp.Description("ID de esta conversación inyectado por Kandel; no uses el ID de la conversación anterior"))
	id := mcp.WithString("id", mcp.Description("ID de la versión exacta del checkpoint"))
	add := func(name, description string, handler server.ToolHandlerFunc, opts ...mcp.ToolOption) {
		if shouldRegister(name, allowlist) {
			srv.AddTool(mcp.NewTool(name, append([]mcp.ToolOption{mcp.WithDescription(description), session}, opts...)...), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				if svc.Source != "" && svc.Sessions != nil {
					s, err := svc.Sessions.Get(ctx, argString(req, "session_id"))
					if err != nil || s == nil || s.Application != svc.Source {
						return mcp.NewToolResultError("usa el session_id actual de esta aplicación"), nil
					}
				}
				return handler(ctx, req)
			})
		}
	}
	add("mem_handoff_get", "Lista los relevos de esta copia de trabajo, o lee el checkpoint indicado por id. Su contenido fue declarado, no es evidencia verificada.", func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sid := argString(r, "session_id")
		if key := argString(r, "id"); key != "" {
			h, err := svc.Handoff.GetSession(ctx, sid, key)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(h)
		}
		hs, err := svc.Handoff.ListSession(ctx, sid)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(hs)
	}, id, mcp.WithReadOnlyHintAnnotation(true))
	add("mem_handoff_resume", "Vincula esta conversación a la versión indicada sin alterarla; devuelve el checkpoint. Verifica el repositorio antes de seguir.", func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h, err := svc.Handoff.Resume(ctx, argString(r, "session_id"), argString(r, "id"))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(h)
	}, mcp.WithString("id", mcp.Required(), mcp.Description("Checkpoint seleccionado")), mcp.WithIdempotentHintAnnotation(true))
	add("mem_handoff_save", "Guarda un avance breve e inmutable durante la tarea: objetivo, estado, pruebas, bloqueos y siguiente paso. No incluyas secretos ni transcripciones.", saveHandoff(svc),
		mcp.WithString("parent_id", mcp.Description("Checkpoint actual de la tarea; omítelo solo al iniciar otra tarea")),
		mcp.WithString("request_id", mcp.Required(), mcp.Description("Clave única de esta escritura; reutilízala solo al reintentar")),
		mcp.WithString("title", mcp.Required()), mcp.WithString("content", mcp.Required()), mcp.WithIdempotentHintAnnotation(true))
}
