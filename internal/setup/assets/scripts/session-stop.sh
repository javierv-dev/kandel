#!/bin/bash
# Kandel — hook Stop para Claude Code (async).
# Marca la sesión como cerrada vía la API HTTP.

KANDEL_PORT="${KANDEL_PORT:-8765}"
KANDEL_URL="http://127.0.0.1:${KANDEL_PORT}"

INPUT=$(cat)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty')

if [ -z "$SESSION_ID" ]; then
  exit 0
fi

curl -sf "${KANDEL_URL}/sessions/${SESSION_ID}/end" -X POST -H 'Content-Type: application/json' -d '{}' \
  >/dev/null 2>&1

exit 0
