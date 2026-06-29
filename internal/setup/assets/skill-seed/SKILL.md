---
name: kandel-seed
description: Puebla la memoria de kandel con las decisiones de alto valor de un proyecto EXISTENTE (arranque en frío). Úsala cuando kandel se acaba de instalar en un repo ya avanzado y la memoria está vacía. Invócala con /kandel-seed.
---

# kandel-seed — poblar la memoria de un proyecto existente

Cuando kandel se instala en un proyecto que ya lleva trabajo hecho, su memoria arranca **vacía**: no
hay decisiones, arquitectura ni convenciones guardadas, así que la primera sesión no tiene contexto
que recuperar. Esta skill resuelve ese arranque en frío.

## Forma recomendada: el comando CLI

La vía canónica es el comando `kandel seed`, que es **agente-independiente** y hace todo el trabajo
(reúne la documentación, extrae las decisiones con un LLM, las guarda con dedupe):

1. Primero en seco, para revisar qué se propondría sin escribir nada:
   ```
   kandel seed --dry-run
   ```
2. Revisa los candidatos con el usuario. Si está de acuerdo, ejecútalo guardando:
   ```
   kandel seed
   ```
   (Pedirá confirmación. Para uso no interactivo: `kandel seed --yes`.)

El comando guarda solo **decisiones de alto valor** (decision/architecture/config/pattern), marca las
memorias con procedencia `kandel-seed`, y es **idempotente**: re-ejecutarlo no duplica.

## Alternativa: hacerlo tú mismo con mem_save

Si el comando no está disponible o el usuario prefiere control fino, hazlo a mano:

1. Lee la documentación del repo: `README`, `docs/`, `CLAUDE.md`/`AGENTS.md`, ADRs, los manifests
   (`go.mod`, `package.json`, …) y el `git log` reciente.
2. Identifica SOLO las decisiones duraderas de alto valor: motor de base de datos elegido y por qué,
   estilo de arquitectura, convenciones, configuración clave, patrones establecidos. **Ignora** estado
   transitorio (TODOs, "siguiente paso", bugs abiertos) y trivialidades.
3. Por cada una, llama a `mem_save` con `type` adecuado (decision/architecture/config/pattern),
   `scope` normalmente `project`, contenido en formato **What/Why/Where/Learned**, un `topic_key`
   estable cuando agrupe un tema, y `tool: "kandel-seed"` para marcar la procedencia.

Calidad sobre cantidad: pocas memorias densas y correctas valen más que muchas triviales. No inventes
decisiones que no estén en la documentación.
