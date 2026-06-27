#!/bin/bash
# Kandel — hook SubagentStop para Claude Code (async).
# Lee la salida del subagente de stdin y la envía a la captura pasiva; toda la extracción de
# aprendizajes vive en el servidor Go (este script es deliberadamente mínimo).

KANDEL_PORT="${KANDEL_PORT:-8765}"
KANDEL_URL="http://127.0.0.1:${KANDEL_PORT}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/_helpers.sh"

INPUT=$(cat)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty')
CWD=$(echo "$INPUT" | jq -r '.cwd // empty')
OUTPUT=$(echo "$INPUT" | jq -r '.stdout // empty')
PROJECT=$(detect_project "$CWD")

[ -z "$OUTPUT" ] && exit 0

curl -sf "${KANDEL_URL}/memories/passive" -X POST -H 'Content-Type: application/json' \
  -d "$(jq -n --arg sid "$SESSION_ID" --arg content "$OUTPUT" --arg project "$PROJECT" --arg source "subagent-stop" \
    '{session_id:$sid, content:$content, project:$project, source:$source}')" \
  >/dev/null 2>&1

exit 0
