package mcp

import (
	"context"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerConflictTools registra las herramientas de conflictos. Solo se montan si hay un
// ConflictService cableado (necesita el AgentRunner, que puede faltar si no hay CLI de agente).
func registerConflictTools(srv *server.MCPServer, svc Services, allowlist map[string]bool) {
	if svc.Conflict == nil {
		return
	}
	if shouldRegister("mem_compare", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_compare",
				mcp.WithDescription("Compara dos memorias por id usando el juez LLM y persiste el veredicto (conflicts_with, supersedes, scoped, related, compatible o not_conflict). Es la operación cara: invoca al agente."),
				mcp.WithTitleAnnotation("Comparar memorias"),
				mcp.WithNumber("source_id", mcp.Required(), mcp.Description("Id de la memoria A")),
				mcp.WithNumber("target_id", mcp.Required(), mcp.Description("Id de la memoria B")),
			),
			handleCompare(svc),
		)
	}
	if shouldRegister("mem_judge", allowlist) {
		srv.AddTool(
			mcp.NewTool("mem_judge",
				mcp.WithDescription("Registra un veredicto que TÚ (el agente) ya decidiste sobre el conflicto entre dos memorias, sin volver a invocar al juez LLM. Usa el vocabulario cerrado de relaciones."),
				mcp.WithTitleAnnotation("Juzgar conflicto"),
				mcp.WithNumber("source_id", mcp.Required(), mcp.Description("Id de la memoria A")),
				mcp.WithNumber("target_id", mcp.Required(), mcp.Description("Id de la memoria B")),
				mcp.WithString("relation", mcp.Required(), mcp.Description("conflicts_with | supersedes | scoped | related | compatible | not_conflict")),
				mcp.WithNumber("confidence", mcp.Description("Confianza 0.0–1.0")),
				mcp.WithString("reasoning", mcp.Description("Justificación breve")),
			),
			handleJudge(svc),
		)
	}
}

// handleCompare traduce mem_compare a Conflict.Compare.
func handleCompare(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sourceID := int64(argInt(req, "source_id", 0))
		targetID := int64(argInt(req, "target_id", 0))
		if sourceID <= 0 || targetID <= 0 {
			return mcp.NewToolResultError("source_id y target_id son obligatorios"), nil
		}
		rel, err := svc.Conflict.Compare(ctx, sourceID, targetID)
		if err != nil {
			return mcp.NewToolResultError("error al comparar: " + err.Error()), nil
		}
		return relationResult(rel)
	}
}

// handleJudge traduce mem_judge a Conflict.RecordVerdict.
func handleJudge(svc Services) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sourceID := int64(argInt(req, "source_id", 0))
		targetID := int64(argInt(req, "target_id", 0))
		if sourceID <= 0 || targetID <= 0 {
			return mcp.NewToolResultError("source_id y target_id son obligatorios"), nil
		}
		relation := domain.RelationType(argString(req, "relation"))
		if !relation.Valid() || relation == domain.RelationPending {
			return mcp.NewToolResultError("relation inválida: usa el vocabulario cerrado"), nil
		}
		rel, err := svc.Conflict.RecordVerdict(ctx, sourceID, targetID, domain.Verdict{
			Relation:   relation,
			Confidence: argFloat(req, "confidence", 0),
			Reasoning:  argString(req, "reasoning"),
		})
		if err != nil {
			return mcp.NewToolResultError("error al juzgar: " + err.Error()), nil
		}
		return relationResult(rel)
	}
}

// relationResult serializa una relación juzgada como resultado JSON.
func relationResult(rel domain.MemoryRelation) (*mcp.CallToolResult, error) {
	return jsonResult(map[string]any{
		"id":         rel.ID,
		"relation":   string(rel.Kind),
		"confidence": rel.Confidence,
		"status":     string(rel.Status),
		"source_id":  rel.SourceID,
		"target_id":  rel.TargetID,
	})
}
