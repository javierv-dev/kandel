#!/bin/bash
# Kandel — hook Stop para Claude Code.
# Wrapper de una línea: toda la lógica vive en el binario (`kandel hook`).
exec kandel hook session-stop
