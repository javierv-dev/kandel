#!/bin/bash
# Kandel — hook UserPromptSubmit para Claude Code.
# Wrapper de una línea: toda la lógica vive en el binario (`kandel hook`). Debe imprimir JSON
# válido por stdout y salir 0 siempre; de eso se encarga `kandel hook`.
exec kandel hook user-prompt-submit
