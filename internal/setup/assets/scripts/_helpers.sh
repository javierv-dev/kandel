#!/bin/bash
# Kandel — helpers compartidos para los hooks de Claude Code.
# AVISO: no leer de stdin aquí — los scripts hacen source de este archivo antes de leer su input.

# Detecta el nombre del proyecto a partir del remoto git, con alternativas.
# Prioridad: nombre del repo del remoto origin > basename de la raíz git > basename del cwd.
detect_project() {
  local dir="$1"

  local url
  url=$(git -C "$dir" remote get-url origin 2>/dev/null)
  if [ -n "$url" ]; then
    local name
    name=$(echo "$url" | sed 's/\.git$//' | sed 's|.*[/:]||' | tr '[:upper:]' '[:lower:]')
    if [ -n "$name" ]; then
      echo "$name"
      return
    fi
  fi

  local root
  root=$(git -C "$dir" rev-parse --show-toplevel 2>/dev/null)
  if [ -n "$root" ]; then
    basename "$root" | tr '[:upper:]' '[:lower:]'
    return
  fi

  basename "$dir" | tr '[:upper:]' '[:lower:]'
}
