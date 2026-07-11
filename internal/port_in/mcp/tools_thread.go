package mcp

import (
	"context"
	"errors"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerThreadTools registra las herramientas de trazabilidad de bitácora: mem_link (enlaza una
// fase con la anterior) y mem_thread (recorre la cadena para reconstruir el hilo).
func registerThreadTools(srv *server.MCPServer, svc Services, allowlist map[string]bool) {
	if svc.Thread == nil {
		return
	}

	if shouldRegister("mem_link", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_link",
				mcp.WithDescription("Enlaza una entrada de bitácora con la fase ANTERIOR del mismo hilo (relación 'continues'). Llámala en la transición de fase: al abrir la bitácora de la fase nueva, enlázala con la que acabas de cerrar. Así mem_thread puede reconstruir después cómo se llegó al estado actual."),
				mcp.WithTitleAnnotation("Enlazar bitácora"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithNumber("from_id", mcp.Required(), mcp.Description("Id de la entrada NUEVA (la fase que continúa)")),
				mcp.WithNumber("to_id", mcp.Required(), mcp.Description("Id de la entrada ANTERIOR (la fase que se continúa)")),
			),
			handleLink(svc),
		)
	}

	if shouldRegister("mem_thread", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_thread",
				mcp.WithDescription("Reconstruye el hilo de bitácora que termina en una entrada, siguiendo la cadena 'continues' hacia atrás. Devuelve las entradas ordenadas de la más antigua a la más nueva, con su cuerpo completo. Úsala para responder '¿cómo llegamos a este estado?' / '¿qué se hizo antes?'."),
				mcp.WithTitleAnnotation("Hilo de bitácora"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithNumber("id", mcp.Required(), mcp.Description("Id de la entrada de bitácora desde la que recorrer el hilo hacia atrás")),
			),
			handleThread(svc),
		)
	}
}

// handleLink crea el enlace 'continues' entre dos entradas de bitácora.
func handleLink(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		from := int64(argInt(req, "from_id", 0))
		to := int64(argInt(req, "to_id", 0))
		if from <= 0 || to <= 0 {
			return mcp.NewToolResultError("from_id y to_id son obligatorios para mem_link"), nil
		}
		rel, err := svc.Thread.Link(ctx, from, to)
		if err != nil {
			switch {
			case errors.Is(err, domain.ErrSelfContinue):
				return mcp.NewToolResultError("una entrada no puede continuarse a sí misma"), nil
			case errors.Is(err, domain.ErrNotFound):
				return mcp.NewToolResultError("alguna de las entradas no existe"), nil
			default:
				return mcp.NewToolResultError("error al enlazar: " + err.Error()), nil
			}
		}
		return jsonResult(map[string]any{
			"linked": true,
			"from":   from,
			"to":     to,
			"kind":   string(rel.Kind),
		})
	}
}

// handleThread recorre la cadena 'continues' y devuelve el hilo ordenado (antigua → nueva).
func handleThread(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(argInt(req, "id", 0))
		if id <= 0 {
			return mcp.NewToolResultError("id es obligatorio para mem_thread"), nil
		}
		chain, err := svc.Thread.Thread(ctx, id)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return mcp.NewToolResultError("la entrada de bitácora no existe"), nil
			}
			return mcp.NewToolResultError("error al recorrer el hilo: " + err.Error()), nil
		}
		items := make([]map[string]any, 0, len(chain))
		for _, m := range chain {
			items = append(items, map[string]any{
				"id":      m.ID,
				"type":    string(m.Type),
				"title":   m.Title,
				"content": m.Content,
			})
		}
		return jsonResult(map[string]any{"thread": items})
	}
}
