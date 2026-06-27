#!/bin/bash
# Kandel — hook SessionStart para Claude Code.
#
# 1. Asegura que el daemon HTTP (kandel serve) está corriendo.
# 2. Crea la sesión en kandel.
# 3. Migra el nombre del proyecto si cambió (basename → nombre del remoto git).
# 4. Importa los chunks sincronizados por git si hay .kandel/manifest.json.
# 5. Inyecta el protocolo de memoria + el contexto de sesiones anteriores.

KANDEL_PORT="${KANDEL_PORT:-8765}"
KANDEL_URL="http://127.0.0.1:${KANDEL_PORT}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/_helpers.sh"

INPUT=$(cat)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty')
CWD=$(echo "$INPUT" | jq -r '.cwd // empty')
OLD_PROJECT=$(basename "$CWD" | tr '[:upper:]' '[:lower:]')
PROJECT=$(detect_project "$CWD")

# Asegura el daemon HTTP.
if ! curl -sf "${KANDEL_URL}/health" --max-time 1 >/dev/null 2>&1; then
  kandel serve >/dev/null 2>&1 &
  sleep 0.5
fi

# Migra el nombre del proyecto si cambió (idempotente; el servidor ignora nombres equivalentes).
if [ "$OLD_PROJECT" != "$PROJECT" ] && [ -n "$OLD_PROJECT" ] && [ -n "$PROJECT" ]; then
  curl -sf "${KANDEL_URL}/projects/migrate" -X POST -H 'Content-Type: application/json' \
    -d "$(jq -n --arg old "$OLD_PROJECT" --arg new "$PROJECT" '{old_project:$old,new_project:$new}')" \
    >/dev/null 2>&1
fi

# Crea la sesión.
if [ -n "$SESSION_ID" ] && [ -n "$PROJECT" ]; then
  curl -sf "${KANDEL_URL}/sessions" -X POST -H 'Content-Type: application/json' \
    -d "$(jq -n --arg id "$SESSION_ID" --arg p "$PROJECT" --arg d "$CWD" '{id:$id,project:$p,directory:$d}')" \
    >/dev/null 2>&1
fi

# Importa los chunks sincronizados por git (en segundo plano, sin bloquear).
if [ -f "${CWD}/.kandel/manifest.json" ]; then
  ( cd "$CWD" 2>/dev/null && kandel sync --import >/dev/null 2>&1 ) &
fi

# Recupera el contexto del proyecto.
ENCODED_PROJECT=$(printf '%s' "$PROJECT" | jq -sRr @uri)
CONTEXT=$(curl -sf "${KANDEL_URL}/context?project=${ENCODED_PROJECT}" --max-time 3 2>/dev/null | jq -r '.context // empty')

# Inyecta el protocolo — stdout llega a Claude como additionalContext.
cat <<'PROTOCOL'
## Kandel Persistent Memory — ACTIVE PROTOCOL

You have kandel memory tools. This protocol is MANDATORY and ALWAYS ACTIVE.

### CORE TOOLS — always available, no ToolSearch needed
mem_save, mem_search, mem_context, mem_session_summary, mem_get, mem_save_prompt

Use ToolSearch for other tools: mem_update, mem_suggest_topic_key, mem_session_start, mem_session_end, mem_delete, mem_capture_passive

### PROACTIVE SAVE — do NOT wait for the user to ask
Call `mem_save` IMMEDIATELY after ANY of these:
- Decision made (architecture, convention, workflow, tool choice)
- Bug fixed (include root cause)
- Convention or workflow documented/updated
- Non-obvious discovery, gotcha, or edge case found
- Pattern established (naming, structure, approach)
- User preference or constraint learned
- User confirms your recommendation or rejects an approach
- A discussion concludes with a clear direction chosen

**Self-check after EVERY task**: "Did I or the user just make a decision, confirm a recommendation, express a preference, fix a bug, learn something, or establish a convention? If yes → mem_save NOW."

### SEARCH MEMORY when:
- The user asks to recall anything ("remember", "what did we do", or the equivalent in their language)
- Starting work on something that might have been done before
- The user's FIRST message references the project, a feature, or a problem — call `mem_search` first

### SESSION CLOSE — before saying "done":
Call `mem_session_summary` with: Goal, Discoveries, Accomplished, Next Steps, Relevant Files.
PROTOCOL

if [ -n "$CONTEXT" ]; then
  printf "\n%s\n" "$CONTEXT"
fi

exit 0
