#!/bin/bash
# Kandel — hook UserPromptSubmit para Claude Code.
#
# - Persiste cada prompt del usuario (POST /prompts), fire-and-forget.
# - En el PRIMER mensaje de la sesión inyecta un ToolSearch para cargar las tools mem_* (que
#   están diferidas por defecto) y pide llamar a mem_context antes de responder.
#
# DEBE salir 0 siempre e imprimir JSON válido — si no, Claude Code bloquea el mensaje.

KANDEL_PORT="${KANDEL_PORT:-8765}"
KANDEL_URL="http://127.0.0.1:${KANDEL_PORT}"

INPUT=$(cat)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)
PROMPT=$(echo "$INPUT" | jq -r '.prompt // empty' 2>/dev/null)

# Persiste el prompt (subshell desacoplado: nunca bloquea el hook).
if [ -n "$PROMPT" ] && [ -n "$SESSION_ID" ]; then
  (
    curl -sf -X POST "${KANDEL_URL}/prompts" --max-time 2 -H 'Content-Type: application/json' \
      -d "$(jq -n --arg s "$SESSION_ID" --arg c "$PROMPT" '{session_id:$s, content:$c}')" \
      >/dev/null 2>&1 || true
  ) &
fi

# Detección de primer mensaje por archivo de estado keyed por session_id.
STATE_FILE="${TMPDIR:-/tmp}/kandel-claude-${SESSION_ID:-$$}-tools-loaded"
if [ ! -f "$STATE_FILE" ]; then
  touch "$STATE_FILE" 2>/dev/null || true
  printf '%s\n' '{"systemMessage":"CRITICAL FIRST ACTION — Execute this ToolSearch NOW before responding:\nselect:mcp__kandel__mem_save,mcp__kandel__mem_search,mcp__kandel__mem_context,mcp__kandel__mem_session_summary,mcp__kandel__mem_get,mcp__kandel__mem_save_prompt,mcp__kandel__mem_capture_passive\n\nAfter loading tools, call mem_context to check for prior session history before responding."}'
  exit 0
fi

printf '%s\n' '{}'
exit 0
