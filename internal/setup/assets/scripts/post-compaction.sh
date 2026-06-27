#!/bin/bash
# Kandel — hook post-compactación para Claude Code.
# Tras una compactación, inyecta el protocolo + contexto e instruye al agente a persistir el
# resumen compactado vía mem_session_summary.

KANDEL_PORT="${KANDEL_PORT:-8765}"
KANDEL_URL="http://127.0.0.1:${KANDEL_PORT}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/_helpers.sh"

INPUT=$(cat)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty')
CWD=$(echo "$INPUT" | jq -r '.cwd // empty')
PROJECT=$(detect_project "$CWD")

# Asegura que la sesión existe.
if [ -n "$SESSION_ID" ] && [ -n "$PROJECT" ]; then
  curl -sf "${KANDEL_URL}/sessions" -X POST -H 'Content-Type: application/json' \
    -d "$(jq -n --arg id "$SESSION_ID" --arg p "$PROJECT" --arg d "$CWD" '{id:$id,project:$p,directory:$d}')" \
    >/dev/null 2>&1
fi

ENCODED_PROJECT=$(printf '%s' "$PROJECT" | jq -sRr @uri)
CONTEXT=$(curl -sf "${KANDEL_URL}/context?project=${ENCODED_PROJECT}" --max-time 3 2>/dev/null | jq -r '.context // empty')

cat <<'PROTOCOL'
## Kandel Persistent Memory — ACTIVE PROTOCOL (post-compaction)

CRITICAL INSTRUCTION POST-COMPACTION — follow these steps IN ORDER:
PROTOCOL

printf "\n1. FIRST: Call mem_session_summary with the content of the compacted summary above. Use project: '%s'.\n" "$PROJECT"
printf "   This preserves what was accomplished before compaction.\n\n"
printf "2. THEN: Call mem_context with project: '%s' to recover recent session history and memories.\n" "$PROJECT"
cat <<'PROTOCOL'
3. If you need more detail on a specific topic, call mem_search with relevant keywords.
4. Only THEN continue working on what the user asked.

All steps are MANDATORY. Without them, you lose context and start blind.
PROTOCOL

if [ -n "$CONTEXT" ]; then
  printf "\n%s\n" "$CONTEXT"
fi

exit 0
