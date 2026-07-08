package mcp

import (
	"context"
	"encoding/json"

	"github.com/javierv-dev/kandel/internal/config"
	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerSyncTools registra mem_sync: sincroniza la memoria con el hub a pedido del agente.
func registerSyncTools(srv *server.MCPServer, svc Services, allowlist map[string]bool) {
	if svc.Sync == nil || !shouldRegister("mem_sync", allowlist) {
		return
	}
	srv.AddTool(
		mcp.NewTool("mem_sync",
			mcp.WithDescription("Sincroniza la memoria con el hub: sube lo local (push) y baja lo de otras máquinas (pull) en una llamada. Úsala cuando el usuario pida sincronizar o cambiar de máquina. Requiere el sync configurado (KANDEL_SYNC_URL + KANDEL_SYNC_TOKEN). Es idempotente: reejecutar no daña (el hub arbitra por LWW)."),
			mcp.WithTitleAnnotation("Sincronizar"),
			mcp.WithIdempotentHintAnnotation(true),
		),
		handleSync(svc),
	)
}

// handleSync corre push + pull SÍNCRONO (el agente lo pidió y espera el resultado, a diferencia de
// los disparos de fondo). Devuelve cuántas filas subieron/bajaron y, si hubo edición concurrente,
// avisa con la lista de memorias en conflicto (no se pisó nada: quedan en la bandeja para resolver).
func handleSync(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !config.SyncConfigured() {
			return mcp.NewToolResultError("sync no configurado: define KANDEL_SYNC_URL y KANDEL_SYNC_TOKEN " +
				"(o `kandel config set sync_url <url>` y `kandel config set sync_token <token>`)"), nil
		}

		push, err := svc.Sync.Push(ctx)
		if err != nil {
			return mcp.NewToolResultError("error al subir (push): " + err.Error()), nil
		}
		pull, err := svc.Sync.Pull(ctx)
		if err != nil {
			return mcp.NewToolResultError("error al bajar (pull): " + err.Error()), nil
		}

		result := map[string]any{
			"pushed":    push.Count,
			"pulled":    pull.Applied,
			"conflicts": pull.Conflicts,
		}
		// Con edición concurrente detectada, adjunta la lista para que el agente la comente al usuario.
		if pull.Conflicts > 0 {
			result["conflict_note"] = "hubo edición concurrente (se editó aquí y en otra máquina a la vez): " +
				"NO se pisó nada, quedan en la bandeja. Resuélvelos con `kandel sync conflicts`."
			if list, err := svc.Sync.ListConflicts(ctx); err == nil {
				items := make([]map[string]any, 0, len(list))
				for _, c := range list {
					items = append(items, map[string]any{
						"sync_id":     c.SyncID,
						"project":     c.Project,
						"incoming_op": string(c.IncomingOp),
						"title":       conflictTitle(c),
					})
				}
				result["conflict_list"] = items
			}
		}
		return jsonResult(result)
	}
}

// conflictTitle intenta leer el título de la memoria remota del payload del conflicto; si no puede,
// describe la operación (espeja incomingTitle de la CLI).
func conflictTitle(c domain.SyncConflict) string {
	if c.IncomingOp == domain.SyncOpDelete {
		return "borrada en la otra máquina"
	}
	var m domain.Memory
	if err := json.Unmarshal(c.Incoming, &m); err != nil || m.Title == "" {
		return "cambio remoto"
	}
	return m.Title
}
