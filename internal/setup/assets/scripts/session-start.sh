#!/bin/bash
# Kandel — hook SessionStart para Claude Code.
# Toda la lógica vive en el binario (`kandel hook`), no en el shell: así la integración no depende
# de jq/curl (que no vienen en Windows) y el binario da seguimiento exacto de cada evento.
exec kandel hook session-start
