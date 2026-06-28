#!/bin/bash
# Kandel — hook SubagentStop para Claude Code.
# Wrapper de una línea: toda la lógica vive en el binario (`kandel hook`).
exec kandel hook subagent-stop
