package mcp

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// allProjects es el comodín que pide las memorias pendientes de TODOS los proyectos.
const allProjects = "*"

// registerLifecycleTools registra las herramientas de ciclo de vida (pendientes por resolver).
func registerLifecycleTools(srv *server.MCPServer, svc Services, allowlist map[string]bool) {
	if shouldRegister("mem_pending", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_pending",
				mcp.WithDescription("Lista las memorias pendientes por resolver (resolution=open), agrupadas por proyecto. "+
					"Pasa el proyecto SIEMPRE: 'project' para uno, 'projects' para varios, o project=\"*\" para todos."),
				mcp.WithTitleAnnotation("Pendientes"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithString("project", mcp.Description(`Proyecto a consultar; "*" = todos los proyectos`)),
				mcp.WithArray("projects", mcp.Description("Varios proyectos a consultar por separado"), mcp.WithStringItems()),
				mcp.WithNumber("limit", mcp.Description("Máximo de memorias por proyecto (def. 20)")),
			),
			handlePending(svc),
		)
	}
}

// handlePending traduce mem_pending a Lifecycle.PendingResolution. Exige el proyecto explícito (no
// hay fallback silencioso a "todos"): con project="*" devuelve todos; con uno o varios proyectos
// devuelve un grupo por cada uno. La salida siempre viene agrupada por proyecto.
func handlePending(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		limit := argInt(req, "limit", 20)
		projects := pendingProjects(req)
		if len(projects) == 0 {
			return mcp.NewToolResultError(`proyecto ambiguo: pasa 'project', 'projects' o project="*" para todos`), nil
		}

		// "*" => todos los proyectos en una sola consulta, agrupados por su propio proyecto.
		for _, p := range projects {
			if p == allProjects {
				memories, err := svc.Lifecycle.PendingResolution(ctx, "", limit)
				if err != nil {
					return mcp.NewToolResultError("error al listar memorias pendientes: " + err.Error()), nil
				}
				return jsonResult(map[string]any{"groups": groupByProject(memories)})
			}
		}

		// Uno o varios proyectos: una consulta por cada uno, un grupo por cada uno (aunque quede vacío).
		groups := make([]map[string]any, 0, len(projects))
		for _, p := range projects {
			memories, err := svc.Lifecycle.PendingResolution(ctx, p, limit)
			if err != nil {
				return mcp.NewToolResultError("error al listar memorias pendientes: " + err.Error()), nil
			}
			groups = append(groups, map[string]any{"project": p, "memories": pendingItems(memories)})
		}
		return jsonResult(map[string]any{"groups": groups})
	}
}

// pendingProjects reúne los proyectos pedidos (de 'projects' y 'project'), recortados y sin
// duplicados, preservando el orden de aparición.
func pendingProjects(req mcp.CallToolRequest) []string {
	raw := argStringList(req, "projects")
	if p := argString(req, "project"); p != "" {
		raw = append(raw, p)
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// groupByProject reparte una lista plana de memorias en grupos por su proyecto, preservando el orden
// de primera aparición de cada proyecto.
func groupByProject(memories []domain.Memory) []map[string]any {
	order := make([]string, 0)
	byProject := make(map[string][]domain.Memory)
	for _, m := range memories {
		if _, ok := byProject[m.Project]; !ok {
			order = append(order, m.Project)
		}
		byProject[m.Project] = append(byProject[m.Project], m)
	}
	groups := make([]map[string]any, 0, len(order))
	for _, p := range order {
		groups = append(groups, map[string]any{"project": p, "memories": pendingItems(byProject[p])})
	}
	return groups
}

// pendingItems proyecta las memorias al resumen que devuelve la tool (id, type, title).
func pendingItems(memories []domain.Memory) []map[string]any {
	items := make([]map[string]any, 0, len(memories))
	for _, m := range memories {
		items = append(items, map[string]any{
			"id":    m.ID,
			"type":  string(m.Type),
			"title": m.Title,
		})
	}
	return items
}
