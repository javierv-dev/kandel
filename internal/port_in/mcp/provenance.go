package mcp

import "context"

// Session links are declared in the MCP call. The application, when available,
// comes from the session registered by the hook, not from the memory contents.
func addProvenance(ctx context.Context, svc Services, item map[string]any, sessionID string) {
	if svc.Sessions == nil {
		return
	}
	item["recorded_by"] = "unknown"
	if sessionID == "" {
		return
	}
	s, err := svc.Sessions.Get(ctx, sessionID)
	if err != nil || s == nil {
		return
	}
	item["session_id"] = s.ID
	if s.Application != "" {
		item["recorded_by"] = s.Application
		item["attribution"] = "declared_session"
	}
}
