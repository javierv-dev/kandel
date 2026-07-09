package mcp

import (
	"context"
	"fmt"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerMemoryTools registra las herramientas que operan sobre memorias.
func registerMemoryTools(srv *server.MCPServer, svc Services, allowlist map[string]bool) {
	if shouldRegister("mem_save", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_save",
				mcp.WithDescription("Guarda una memoria importante de forma persistente. Llámala PROACTIVAMENTE tras completar trabajo significativo (decisiones, arreglos de bugs, patrones, configuración, descubrimientos), no esperes a que te lo pidan."),
				mcp.WithTitleAnnotation("Guardar memoria"),
				mcp.WithReadOnlyHintAnnotation(false),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithString("title", mcp.Required(), mcp.Description("Título corto y buscable")),
				mcp.WithString("content", mcp.Required(), mcp.Description("Contenido, idealmente con formato **What**/**Why**/**Where**/**Learned**")),
				mcp.WithString("type", mcp.Description("Tipo: decision, preference, architecture, bugfix, discovery, journal (entrada de bitácora)")),
				mcp.WithString("scope", mcp.Description("Alcance: project (def.) o global")),
				mcp.WithString("project", mcp.Description(`Proyecto de la memoria. Obligatorio para scope=project (si falta, error); en scope=global se ignora (la global se desancla del proyecto). Usa el "Proyecto actual" inyectado.`)),
				mcp.WithString("session_id", mcp.Description("Id de sesión a asociar")),
				mcp.WithString("tool", mcp.Description("Herramienta que originó la memoria (procedencia), p. ej. kandel-seed")),
				mcp.WithString("resolution", mcp.Description("Estado de pendiente: open (pendiente) o resolved; vacío = normal")),
			),
			handleSave(svc),
		)
	}

	if shouldRegister("mem_search", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_search",
				mcp.WithDescription("Busca en tu memoria persistente por SIGNIFICADO (búsqueda semántica). Úsala para encontrar decisiones, bugs, patrones o contexto de sesiones anteriores aunque no recuerdes las palabras exactas."),
				mcp.WithTitleAnnotation("Buscar memoria"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithString("query", mcp.Required(), mcp.Description("Consulta en lenguaje natural")),
				mcp.WithString("type", mcp.Description("Filtrar por tipo")),
				mcp.WithString("project", mcp.Required(), mcp.Description(`Proyecto a buscar (obligatorio); "*" = todos. Las globales (scope=global) siempre acompañan al proyecto. Usa el "Proyecto actual" inyectado.`)),
				mcp.WithString("scope", mcp.Description("Filtrar por alcance: project o global")),
				mcp.WithNumber("limit", mcp.Description("Máximo de resultados (def. 10)")),
				mcp.WithBoolean("include_superseded", mcp.Description("Incluir memorias reemplazadas; por defecto se ocultan")),
			),
			handleSearch(svc),
		)
	}

	if shouldRegister("mem_context", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_context",
				mcp.WithDescription("Recupera las memorias más recientes (lectura directa, sin búsqueda). Llámala al inicio de sesión o tras una compactación para recuperar el contexto previo."),
				mcp.WithTitleAnnotation("Contexto reciente"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithString("project", mcp.Required(), mcp.Description(`Proyecto a recuperar (obligatorio); "*" = todos. Las globales (scope=global) siempre acompañan al proyecto. Usa el "Proyecto actual" inyectado.`)),
				mcp.WithString("scope", mcp.Description("Filtrar por alcance")),
				mcp.WithNumber("limit", mcp.Description("Máximo de memorias (def. 20)")),
			),
			handleContext(svc),
		)
	}

	if shouldRegister("mem_get", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_get",
				mcp.WithDescription("Obtiene el contenido completo (sin truncar) de una memoria por su id, normalmente tras un mem_search."),
				mcp.WithTitleAnnotation("Obtener memoria"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithNumber("id", mcp.Required(), mcp.Description("Id de la memoria")),
			),
			handleGet(svc),
		)
	}

	if shouldRegister("mem_update", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_update",
				mcp.WithDescription("Actualiza campos de una memoria existente por id. Úsala cuando tengas un id exacto que corregir."),
				mcp.WithTitleAnnotation("Actualizar memoria"),
				mcp.WithNumber("id", mcp.Required(), mcp.Description("Id de la memoria")),
				mcp.WithString("title", mcp.Description("Nuevo título")),
				mcp.WithString("content", mcp.Description("Nuevo contenido")),
				mcp.WithString("type", mcp.Description("Nuevo tipo")),
				mcp.WithString("scope", mcp.Description("Nuevo alcance")),
				mcp.WithString("resolution", mcp.Description("Estado de pendiente: open (pendiente) o resolved; vacío = normal")),
			),
			handleUpdate(svc),
		)
	}

	if shouldRegister("mem_delete", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_delete",
				mcp.WithDescription("⚠️ ÚSALA CON PRECAUCIÓN. Borra una memoria por id (borrado LÓGICO y reversible: deja de aparecer en búsquedas y listados). Es reversible con mem_restore. Solo bórrala si estás seguro (memoria errónea, duplicada o que el usuario pidió olvidar). No existe borrado físico: toda eliminación es reversible."),
				mcp.WithTitleAnnotation("Borrar memoria (lógico)"),
				mcp.WithDestructiveHintAnnotation(true),
				mcp.WithNumber("id", mcp.Required(), mcp.Description("Id de la memoria a borrar")),
			),
			handleDelete(svc),
		)
	}

	if shouldRegister("mem_restore", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_restore",
				mcp.WithDescription("Restaura una memoria borrada lógicamente (deshace mem_delete) por su id. Vuelve a aparecer en búsquedas y listados."),
				mcp.WithTitleAnnotation("Restaurar memoria"),
				mcp.WithNumber("id", mcp.Required(), mcp.Description("Id de la memoria a restaurar")),
			),
			handleRestore(svc),
		)
	}
}

// resolveReadProject aplica la política de lectura estricta de mem_search/mem_context: exige el
// proyecto explícito. NO hay "proyecto del proceso" al que caer: el servidor MCP es compartido y no
// conoce el CWD de cada conversación (el agente recibe el proyecto en el contexto inyectado y debe
// pasarlo). Devuelve (proyecto, true) para uno concreto, ("", true) para el comodín "*" (todos los
// proyectos, sin filtro) y ("", false) si se omitió — el handler lo traduce a un error de ambigüedad.
func resolveReadProject(req mcp.CallToolRequest) (string, bool) {
	p := argString(req, "project")
	if p == "" {
		return "", false
	}
	if p == allProjects {
		return "", true
	}
	return p, true
}

// handleSave traduce la llamada mem_save al caso de uso Memory.Save.
func handleSave(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		title := argString(req, "title")
		content := argString(req, "content")
		if content == "" {
			return mcp.NewToolResultError("content es obligatorio para mem_save"), nil
		}
		// El scope solo se fija si el llamante lo pasa; si se omite se deja vacío para que
		// MemoryService.Save aplique DefaultScopeForType (p. ej. preference→global). Parsear el vacío
		// con ParseScope lo forzaría a project y anularía ese default.
		var scope domain.Scope
		if raw := argString(req, "scope"); raw != "" {
			s, err := domain.ParseScope(raw)
			if err != nil {
				return mcp.NewToolResultError("scope inválido: " + err.Error()), nil
			}
			scope = s
		}
		typ := domain.MemoryType(argString(req, "type"))
		if typ == "" {
			typ = domain.TypeDiscovery
		}
		resolution := domain.Resolution(argString(req, "resolution"))
		if !resolution.Valid() {
			return mcp.NewToolResultError("resolution inválida: usa open, resolved o vacío"), nil
		}
		res, err := svc.Memory.Save(ctx, service.SaveInput{
			SessionID:  argString(req, "session_id"),
			Project:    argString(req, "project"),
			Type:       typ,
			Scope:      scope,
			Title:      title,
			Content:    content,
			ToolName:   argString(req, "tool"),
			Resolution: resolution,
		})
		if err != nil {
			return mcp.NewToolResultError("error al guardar: " + err.Error()), nil
		}
		out := map[string]any{
			"id":      res.Memory.ID,
			"outcome": string(res.Outcome),
			"title":   res.Memory.Title,
		}
		if res.EmbeddingDeferred {
			// La memoria se guardó, pero sin vector (embebedor caído): se avisa al agente para que
			// sepa que aún no aparecerá en la búsqueda semántica hasta que el backfill la procese.
			out["embedding_deferred"] = true
			out["note"] = "guardada SIN vector (embebedor no disponible); pendiente de backfill, aún no aparece en mem_search"
		}
		return jsonResult(out)
	}
}

// handleSearch traduce la llamada mem_search a la búsqueda semántica Memory.Search.
func handleSearch(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := argString(req, "query")
		if query == "" {
			return mcp.NewToolResultError("query es obligatorio para mem_search"), nil
		}
		var scope domain.Scope
		if raw := argString(req, "scope"); raw != "" {
			s, err := domain.ParseScope(raw)
			if err != nil {
				return mcp.NewToolResultError("scope inválido: " + err.Error()), nil
			}
			scope = s
		}
		project, ok := resolveReadProject(req)
		if !ok {
			return mcp.NewToolResultError(`proyecto ambiguo: pasa 'project' o project="*" para buscar en todos`), nil
		}
		results, err := svc.Memory.Search(ctx, ports.SearchQuery{
			Query:             query,
			Type:              domain.MemoryType(argString(req, "type")),
			Project:           project,
			Scope:             scope,
			Limit:             argInt(req, "limit", 10),
			IncludeSuperseded: argBool(req, "include_superseded", false),
		})
		if err != nil {
			return mcp.NewToolResultError("error de búsqueda: " + err.Error()), nil
		}
		items := make([]map[string]any, 0, len(results))
		for _, r := range results {
			items = append(items, map[string]any{
				"id":    r.Memory.ID,
				"score": r.Score,
				"type":  string(r.Memory.Type),
				"title": r.Memory.Title,
			})
		}
		return jsonResult(map[string]any{"results": items})
	}
}

// handleContext traduce la llamada mem_context a la lectura directa Memory.Recent.
func handleContext(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var scope domain.Scope
		if raw := argString(req, "scope"); raw != "" {
			s, err := domain.ParseScope(raw)
			if err != nil {
				return mcp.NewToolResultError("scope inválido: " + err.Error()), nil
			}
			scope = s
		}
		project, ok := resolveReadProject(req)
		if !ok {
			return mcp.NewToolResultError(`proyecto ambiguo: pasa 'project' o project="*" para todos`), nil
		}
		memories, err := svc.Memory.Recent(ctx, ports.RecentQuery{
			Project: project,
			Scope:   scope,
			Limit:   argInt(req, "limit", 20),
		})
		if err != nil {
			return mcp.NewToolResultError("error al recuperar el contexto: " + err.Error()), nil
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

// handleGet traduce la llamada mem_get a Memory.Get.
func handleGet(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(argInt(req, "id", 0))
		if id <= 0 {
			return mcp.NewToolResultError("id es obligatorio para mem_get"), nil
		}
		m, err := svc.Memory.Get(ctx, id)
		if err != nil {
			return mcp.NewToolResultError("error al obtener la memoria: " + err.Error()), nil
		}
		if m == nil {
			return mcp.NewToolResultError(fmt.Sprintf("memoria #%d no encontrada", id)), nil
		}
		return jsonResult(map[string]any{
			"id":      m.ID,
			"type":    string(m.Type),
			"scope":   string(m.Scope),
			"project": m.Project,
			"title":   m.Title,
			"content": m.Content,
		})
	}
}

// handleUpdate traduce la llamada mem_update a Memory.Update (campos parciales).
func handleUpdate(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(argInt(req, "id", 0))
		if id <= 0 {
			return mcp.NewToolResultError("id es obligatorio para mem_update"), nil
		}
		var in service.UpdateInput
		got := false
		if _, ok := req.GetArguments()["title"]; ok {
			v := argString(req, "title")
			in.Title = &v
			got = true
		}
		if _, ok := req.GetArguments()["content"]; ok {
			v := argString(req, "content")
			in.Content = &v
			got = true
		}
		if _, ok := req.GetArguments()["type"]; ok {
			v := domain.MemoryType(argString(req, "type"))
			in.Type = &v
			got = true
		}
		if _, ok := req.GetArguments()["scope"]; ok {
			s, err := domain.ParseScope(argString(req, "scope"))
			if err != nil {
				return mcp.NewToolResultError("scope inválido: " + err.Error()), nil
			}
			in.Scope = &s
			got = true
		}
		if _, ok := req.GetArguments()["resolution"]; ok {
			res := domain.Resolution(argString(req, "resolution"))
			if !res.Valid() {
				return mcp.NewToolResultError("resolution inválida: usa open, resolved o vacío"), nil
			}
			in.Resolution = &res
			got = true
		}
		if !got {
			return mcp.NewToolResultError("proporciona al menos un campo a actualizar"), nil
		}
		m, err := svc.Memory.Update(ctx, id, in)
		if err != nil {
			return mcp.NewToolResultError("error al actualizar la memoria: " + err.Error()), nil
		}
		return jsonResult(map[string]any{"id": m.ID, "title": m.Title})
	}
}

// handleDelete traduce la llamada mem_delete a Memory.Delete. El borrado es siempre LÓGICO
// (reversible con mem_restore): no existe borrado físico en ninguna superficie.
func handleDelete(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(argInt(req, "id", 0))
		if id <= 0 {
			return mcp.NewToolResultError("id es obligatorio para mem_delete"), nil
		}
		if err := svc.Memory.Delete(ctx, id); err != nil {
			return mcp.NewToolResultError("error al borrar la memoria: " + err.Error()), nil
		}
		return jsonResult(map[string]any{"id": id, "delete": "soft", "restore_with": "mem_restore"})
	}
}

// handleRestore traduce la llamada mem_restore a Memory.Restore (deshace un soft-delete).
func handleRestore(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(argInt(req, "id", 0))
		if id <= 0 {
			return mcp.NewToolResultError("id es obligatorio para mem_restore"), nil
		}
		m, err := svc.Memory.Restore(ctx, id)
		if err != nil {
			return mcp.NewToolResultError("error al restaurar la memoria: " + err.Error()), nil
		}
		return jsonResult(map[string]any{"id": m.ID, "title": m.Title, "restored": true})
	}
}
