package mcp

import (
	"context"
	"strconv"
	"strings"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/learnings"
	"github.com/javierv-dev/kandel/internal/project"
	"github.com/javierv-dev/kandel/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerContextTools registra herramientas de contexto: guardado de prompts, captura pasiva de
// aprendizajes y detección del proyecto actual.
func registerContextTools(srv *server.MCPServer, svc Services, allowlist map[string]bool) {
	if shouldRegister("mem_save_prompt", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_save_prompt",
				mcp.WithDescription("Guarda el prompt del usuario como contexto crudo recuperable (separado de las memorias curadas). Es idempotente: un prompt idéntico de la misma sesión no se duplica."),
				mcp.WithTitleAnnotation("Guardar prompt"),
				mcp.WithString("content", mcp.Required(), mcp.Description("Texto del prompt")),
				mcp.WithString("session_id", mcp.Description("Id de sesión a asociar")),
				mcp.WithString("project", mcp.Description("Proyecto; si se omite, el del proceso")),
			),
			handleSavePrompt(svc),
		)
	}

	if shouldRegister("mem_capture_passive", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_capture_passive",
				mcp.WithDescription("Extrae aprendizajes de un texto que incluya una sección '## Key Learnings:' (o 'Aprendizajes:') y guarda cada punto como una memoria de tipo learning. Devuelve cuántos se extrajeron y guardaron."),
				mcp.WithTitleAnnotation("Captura pasiva"),
				mcp.WithString("content", mcp.Required(), mcp.Description("Texto con una sección de aprendizajes en viñetas")),
				mcp.WithString("session_id", mcp.Description("Id de sesión a asociar")),
				mcp.WithString("project", mcp.Description("Proyecto; si se omite, el del proceso")),
			),
			handleCapturePassive(svc),
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
			Project:   svc.resolveProject(argString(req, "project")),
			Content:   content,
		})
		if err != nil {
			return mcp.NewToolResultError("error al guardar el prompt: " + err.Error()), nil
		}
		return jsonResult(map[string]any{"id": res.Prompt.ID, "inserted": res.Inserted})
	}
}

// handleCapturePassive extrae los aprendizajes en viñetas de la sección "Key Learnings" del
// contenido y guarda cada uno como una memoria. La extracción vive aquí (en el adaptador) porque
// es traducción de entrada; el guardado usa el caso de uso Memory.Save.
func handleCapturePassive(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content := argString(req, "content")
		if content == "" {
			return mcp.NewToolResultError("content es obligatorio para mem_capture_passive"), nil
		}
		items := learnings.Extract(content)
		if len(items) == 0 {
			return mcp.NewToolResultError("no se encontró una sección de aprendizajes ('## Key Learnings:' o 'Aprendizajes:') con viñetas"), nil
		}
		project := svc.resolveProject(argString(req, "project"))
		sessionID := argString(req, "session_id")
		saved := 0
		for _, text := range items {
			_, err := svc.Memory.Save(ctx, service.SaveInput{
				SessionID: sessionID,
				Project:   project,
				Type:      domain.TypeDiscovery,
				Scope:     domain.ScopeProject,
				Title:     learnings.Truncate(text, 80),
				Content:   text,
			})
			if err != nil {
				// Un fallo en uno (p. ej. embebedor caído) corta: se informa lo guardado hasta aquí.
				return mcp.NewToolResultError("captura interrumpida tras guardar " + strconv.Itoa(saved) + ": " + err.Error()), nil
			}
			saved++
		}
		return jsonResult(map[string]any{"extracted": len(items), "saved": saved})
	}
}

// handleCurrentProject detecta el proyecto del directorio de trabajo. Un override de proceso
// (KANDEL_PROJECT o --project) gana sobre la detección automática.
func handleCurrentProject(svc Services) server.ToolHandlerFunc {
	return func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		det := project.DetectCWD()
		result := map[string]any{
			"project":        det.Project,
			"project_source": string(det.Source),
			"project_path":   det.Path,
		}
		if p := strings.TrimSpace(svc.DefaultProject); p != "" {
			result["project"] = p
			result["project_source"] = "process_override"
		}
		return jsonResult(result)
	}
}
