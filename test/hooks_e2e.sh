#!/bin/bash
# Harness E2E de los hooks de kandel (camino B: la lógica vive en `kandel hook`).
#
# Prueba el flujo REAL de extremo a extremo SIN Claude Code y SIN jq:
#   binario kandel  ->  `kandel hook <evento>`  ->  daemon HTTP  ->  SQLite
#
# Verifica el bug original (sesiones y prompts no se guardaban): tras disparar los hooks con el
# JSON que envía el agente, la sesión y el prompt DEBEN quedar persistidos. Todo en un entorno
# aislado (KANDEL_DATA_DIR / KANDEL_LOG_DIR / KANDEL_PORT propios), sin tocar ~/.kandel del usuario.
#
# No requiere Ollama: session-start, user-prompt-submit y session-stop no usan el embebedor.
#
# Uso:  bash test/hooks_e2e.sh
set -u

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GO="${GO:-/c/Program Files/Go/bin/go.exe}"
PORT="${KANDEL_E2E_PORT:-8799}"          # puerto propio para no chocar con el daemon real (8765)
WORK="$(mktemp -d)"
BIN="$WORK/kandel.exe"
export KANDEL_DATA_DIR="$WORK/data"
export KANDEL_LOG_DIR="$WORK/logs"
export KANDEL_PORT="$PORT"
URL="http://127.0.0.1:$PORT"
SESSION_ID="e2e-$$-$(date +%s)"
PROJECT_DIR="$WORK/proyecto-demo"
mkdir -p "$KANDEL_DATA_DIR" "$KANDEL_LOG_DIR" "$PROJECT_DIR"

PASS=0
FAIL=0
ok()   { echo "  ✓ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ✗ $1"; FAIL=$((FAIL+1)); }

cleanup() {
  [ -n "${SERVE_PID:-}" ] && kill "$SERVE_PID" >/dev/null 2>&1
  rm -rf "$WORK" >/dev/null 2>&1
}
trap cleanup EXIT

echo "== Build =="
if ! CGO_ENABLED=0 "$GO" build -o "$BIN" "$REPO_ROOT/cmd/kandel" 2>&1; then
  echo "build falló"; exit 1
fi
ok "binario compilado en $BIN"

echo "== Daemon =="
# Se arranca aquí (PID conocido para limpieza). El hook lo encontrará vivo vía /health.
"$BIN" serve >"$KANDEL_LOG_DIR/serve.log" 2>&1 &
SERVE_PID=$!
for i in $(seq 1 20); do
  if curl -sf "$URL/health" --max-time 1 >/dev/null 2>&1; then break; fi
  sleep 0.3
done
if curl -sf "$URL/health" --max-time 1 >/dev/null 2>&1; then
  ok "daemon arriba en $URL"
else
  bad "daemon no respondió"; exit 1
fi

echo "== Hook: session-start =="
printf '{"session_id":"%s","cwd":"%s"}' "$SESSION_ID" "$PROJECT_DIR" \
  | "$BIN" hook session-start >"$WORK/out_start.txt" 2>&1
grep -q "Kandel Persistent Memory" "$WORK/out_start.txt" \
  && ok "imprime el protocolo de memoria" \
  || bad "no imprimió el protocolo"

echo "== Hook: user-prompt-submit =="
PROMPT_TEXT="probando la persistencia de prompts"
printf '{"session_id":"%s","prompt":"%s"}' "$SESSION_ID" "$PROMPT_TEXT" \
  | "$BIN" hook user-prompt-submit >"$WORK/out_prompt.txt" 2>&1
grep -q "additionalContext" "$WORK/out_prompt.txt" \
  && ok "primer mensaje inyecta carga de tools" \
  || bad "no inyectó additionalContext en el primer mensaje"

echo "== Hook: session-stop =="
printf '{"session_id":"%s"}' "$SESSION_ID" | "$BIN" hook session-stop >/dev/null 2>&1
ok "session-stop ejecutado"

echo "== Verificación contra el backend =="
# 1) La sesión existe (GET /sessions/{id} -> 200 y contiene el id).
CODE=$(curl -s -o "$WORK/sess.json" -w '%{http_code}' "$URL/sessions/$SESSION_ID")
if [ "$CODE" = "200" ] && grep -q "$SESSION_ID" "$WORK/sess.json"; then
  ok "sesión persistida (GET /sessions/$SESSION_ID -> 200)"
else
  bad "la sesión NO se persistió (HTTP $CODE)"
fi

# 2) El log de hooks muestra session_id REAL (no '?') y HTTP 201 en sesión y prompt.
LOG="$KANDEL_LOG_DIR/hooks.log"
grep -q "POST /sessions -> HTTP 201" "$LOG" \
  && ok "log: POST /sessions -> HTTP 201" \
  || bad "log: no se registró POST /sessions 201"
grep -q "POST /prompts session_id=$SESSION_ID -> HTTP 201" "$LOG" \
  && ok "log: prompt guardado con session_id real -> HTTP 201" \
  || bad "log: el prompt no se guardó (revisa $LOG)"
if grep -q "no se crea sesión" "$LOG" || grep -q "no se persiste prompt" "$LOG"; then
  bad "el log aún reporta campos vacíos (regresión del bug de jq)"
else
  ok "el log NO reporta campos vacíos (bug de jq resuelto)"
fi

echo
echo "== Resultado: $PASS OK, $FAIL FALLOS =="
echo "(log de hooks: $LOG)"
echo "----- hooks.log -----"
cat "$LOG"
[ "$FAIL" -eq 0 ]
