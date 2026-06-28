package mcp

import (
	"context"
	"fmt"
	"strings"

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
				mcp.WithString("type", mcp.Description("Tipo: decision, architecture, bugfix, pattern, config, discovery, learning")),
				mcp.WithString("scope", mcp.Description("Alcance: project (def.), personal o global")),
				mcp.WithString("topic_key", mcp.Description("topic_key opcional para fusionar revisiones del mismo tema (p. ej. architecture/auth-model)")),
				mcp.WithString("project", mcp.Description("Proyecto explícito; si se omite, se usa el del proceso")),
				mcp.WithString("session_id", mcp.Description("Id de sesión a asociar")),
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
				mcp.WithString("project", mcp.Description("Filtrar por proyecto; si se omite, el del proceso")),
				mcp.WithString("scope", mcp.Description("Filtrar por alcance: project, personal o global")),
				mcp.WithNumber("limit", mcp.Description("Máximo de resultados (def. 10)")),
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
				mcp.WithString("project", mcp.Description("Filtrar por proyecto; si se omite, el del proceso")),
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
				mcp.WithString("topic_key", mcp.Description("Nuevo topic_key (cadena vacía lo quita)")),
			),
			handleUpdate(svc),
		)
	}

	if shouldRegister("mem_suggest_topic_key", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_suggest_topic_key",
				mcp.WithDescription("Sugiere un topic_key estable (kebab-case, familia/descripción) a partir del tipo y el título, para usarlo en upserts con mem_save."),
				mcp.WithTitleAnnotation("Sugerir topic_key"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithString("type", mcp.Description("Tipo de la memoria")),
				mcp.WithString("title", mcp.Required(), mcp.Description("Título de la memoria")),
			),
			handleSuggestTopicKey(),
		)
	}

	if shouldRegister("mem_delete", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_delete",
				mcp.WithDescription("⚠️ ÚSALA CON PRECAUCIÓN. Borra una memoria por id (borrado LÓGICO y reversible: deja de aparecer en búsquedas y listados). Es reversible con mem_restore. Solo bórrala si estás seguro (memoria errónea, duplicada o que el usuario pidió olvidar). El borrado físico irreversible NO está disponible aquí; solo por la CLI (`kandel delete <id> --hard`)."),
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

// handleSave traduce la llamada mem_save al caso de uso Memory.Save.
func handleSave(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		title := argString(req, "title")
		content := argString(req, "content")
		if content == "" {
			return mcp.NewToolResultError("content es obligatorio para mem_save"), nil
		}
		scope, err := domain.ParseScope(argString(req, "scope"))
		if err != nil {
			return mcp.NewToolResultError("scope inválido: " + err.Error()), nil
		}
		typ := domain.MemoryType(argString(req, "type"))
		if typ == "" {
			typ = domain.TypeLearning
		}
		res, err := svc.Memory.Save(ctx, service.SaveInput{
			SessionID: argString(req, "session_id"),
			Project:   svc.resolveProject(argString(req, "project")),
			Type:      typ,
			Scope:     scope,
			Title:     title,
			Content:   content,
			TopicKey:  argString(req, "topic_key"),
		})
		if err != nil {
			return mcp.NewToolResultError("error al guardar: " + err.Error()), nil
		}
		return jsonResult(map[string]any{
			"id":      res.Memory.ID,
			"outcome": string(res.Outcome),
			"title":   res.Memory.Title,
		})
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
		results, err := svc.Memory.Search(ctx, ports.SearchQuery{
			Query:   query,
			Type:    domain.MemoryType(argString(req, "type")),
			Project: svc.resolveProject(argString(req, "project")),
			Scope:   scope,
			Limit:   argInt(req, "limit", 10),
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
		memories, err := svc.Memory.Recent(ctx, ports.RecentQuery{
			Project: svc.resolveProject(argString(req, "project")),
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
			"id":        m.ID,
			"type":      string(m.Type),
			"scope":     string(m.Scope),
			"project":   m.Project,
			"topic_key": string(m.TopicKey),
			"title":     m.Title,
			"content":   m.Content,
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
		if _, ok := req.GetArguments()["topic_key"]; ok {
			v := argString(req, "topic_key")
			in.TopicKey = &v
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

// handleDelete traduce la llamada mem_delete a Memory.Delete. Solo permite borrado LÓGICO
// (reversible) desde el agente; el borrado físico (hard) queda reservado a la CLI por seguridad.
func handleDelete(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := int64(argInt(req, "id", 0))
		if id <= 0 {
			return mcp.NewToolResultError("id es obligatorio para mem_delete"), nil
		}
		if err := svc.Memory.Delete(ctx, id, false); err != nil {
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

// handleSuggestTopicKey deriva un topic_key válido a partir del tipo y el título, sin tocar la
// base de datos. Es una utilidad pura: kebab-case con familia (el tipo) y descripción (el título).
func handleSuggestTopicKey() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		title := argString(req, "title")
		if title == "" {
			return mcp.NewToolResultError("title es obligatorio para sugerir un topic_key"), nil
		}
		family := slugify(argString(req, "type"))
		if family == "" {
			family = "topic"
		}
		desc := slugify(title)
		if desc == "" {
			return mcp.NewToolResultError("no se pudo derivar un topic_key del título"), nil
		}
		candidate := family + "/" + desc
		tk, err := domain.ParseTopicKey(candidate)
		if err != nil {
			return mcp.NewToolResultError("topic_key derivado inválido: " + err.Error()), nil
		}
		return jsonResult(map[string]any{"topic_key": string(tk)})
	}
}

// slugify convierte un texto en un segmento kebab-case: minúsculas, espacios y signos a guiones,
// sin guiones repetidos ni en los extremos.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
