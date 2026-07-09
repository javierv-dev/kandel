package mcp

import (
	"context"

	"github.com/javierv-dev/kandel/internal/project"
	"github.com/javierv-dev/kandel/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerContextTools registra herramientas de contexto: guardado de prompts y detección del
// proyecto actual.
func registerContextTools(srv *server.MCPServer, svc Services, allowlist map[string]bool) {
	if shouldRegister("mem_save_prompt", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_save_prompt",
				mcp.WithDescription("Guarda el prompt del usuario como contexto crudo recuperable (separado de las memorias curadas). Es idempotente: un prompt idéntico de la misma sesión no se duplica."),
				mcp.WithTitleAnnotation("Guardar prompt"),
				mcp.WithString("content", mcp.Required(), mcp.Description("Texto del prompt")),
				mcp.WithString("session_id", mcp.Description("Id de sesión a asociar")),
				mcp.WithString("project", mcp.Description(`Proyecto; usa el "Proyecto actual" inyectado. Si se omite queda sin proyecto (no cae a ninguno).`)),
			),
			handleSavePrompt(svc),
		)
	}

	if shouldRegister("mem_current_project", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_current_project",
				mcp.WithDescription("Detecta el proyecto actual a partir del directorio de trabajo (raíz git o basename). Llamada recomendada al inicio para saber sobre qué proyecto trabajas."),
				mcp.WithTitleAnnotation("Proyecto actual"),
				mcp.WithReadOnlyHintAnnotation(true),
			),
			handleCurrentProject(svc),
		)
	}
}

// handleSavePrompt traduce la llamada mem_save_prompt a Prompt.Save.
func handleSavePrompt(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content := argString(req, "content")
		if content == "" {
			return mcp.NewToolResultError("content es obligatorio para mem_save_prompt"), nil
		}
		res, err := svc.Prompt.Save(ctx, service.SavePromptInput{
			SessionID: argString(req, "session_id"),
			Project:   argString(req, "project"),
			Content:   content,
		})
		if err != nil {
			return mcp.NewToolResultError("error al guardar el prompt: " + err.Error()), nil
		}
		return jsonResult(map[string]any{"id": res.Prompt.ID, "inserted": res.Inserted})
	}
}

// handleCurrentProject detecta el proyecto del directorio de trabajo (raíz git o basename). Existe
// para agentes sin el hook de kandel; con el hook, el proyecto ya viene inyectado en la conversación.
func handleCurrentProject(_ Services) server.ToolHandlerFunc {
	return func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		det := project.DetectCWD()
		return jsonResult(map[string]any{
			"project":        det.Project,
			"project_source": string(det.Source),
			"project_path":   det.Path,
		})
	}
}
