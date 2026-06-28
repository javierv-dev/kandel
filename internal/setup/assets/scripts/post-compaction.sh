#!/bin/bash
# Kandel — hook post-compactación para Claude Code.
# Wrapper de una línea: toda la lógica vive en el binario (`kandel hook`).
exec kandel hook post-compaction
