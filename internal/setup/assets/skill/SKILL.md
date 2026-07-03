---
name: kandel-memory
description: "SIEMPRE ACTIVO — Protocolo de memoria persistente. DEBES guardar decisiones, convenciones, bugs y descubrimientos en kandel de forma proactiva. NO esperes a que el usuario lo pida."
---

# Memoria persistente kandel — Protocolo

Tienes acceso a kandel, un sistema de memoria persistente que sobrevive entre sesiones y compactaciones.
Este protocolo es OBLIGATORIO y SIEMPRE ACTIVO — no es algo que actives a demanda.

## HERRAMIENTAS DISPONIBLES

Las tools de kandel vienen de un servidor MCP y sus esquemas están **diferidos**: DEBES cargarlas
con `ToolSearch` antes de llamar cualquier tool `mem_*` (los hooks SessionStart/UserPromptSubmit te
lo recuerdan). Carga el conjunto principal con UN SOLO ToolSearch la primera vez que la memoria sea relevante:

```
ToolSearch select:mcp__kandel__mem_save,mcp__kandel__mem_search,mcp__kandel__mem_context,mcp__kandel__mem_session_summary,mcp__kandel__mem_get,mcp__kandel__mem_save_prompt
```

Tools principales:
- `mem_save`, `mem_search`, `mem_context`, `mem_session_summary`
- `mem_get`, `mem_update`
- `mem_session_start`, `mem_session_end`, `mem_save_prompt`

**Plan B**: si las tools no están disponibles inesperadamente, ejecuta `kandel install claude-code`
otra vez y reinicia Claude Code. Repara la config del MCP, los hooks y la skill.

Tools adicionales (diferidas — cárgalas con ToolSearch solo cuando las necesites):
- `mem_pending`, `mem_capture_passive`, `mem_current_project`
- `mem_doctor`, `mem_compare`, `mem_judge`
- `mem_delete`, `mem_restore`

> ⚠️ `mem_delete` borra una memoria (borrado LÓGICO — reversible con `mem_restore`). Úsala
> SOLO cuando estés seguro: una memoria errónea o duplicada, o una que el usuario pidió olvidar.
> No existe borrado físico: toda eliminación es reversible.

## DISPARADORES DE GUARDADO PROACTIVO (obligatorio — NO esperes a que el usuario lo pida)

Llama a `mem_save` INMEDIATAMENTE y SIN QUE TE LO PIDAN tras cualquiera de estos:

### Tras decisiones o convenciones
- Se toma una decisión de arquitectura o diseño
- Se documenta o establece una convención del equipo
- Se acuerda un cambio de flujo de trabajo
- Se elige una herramienta o librería con sus trade-offs

### Tras completar trabajo
- Se termina un arreglo de bug (incluye la causa raíz)
- Se implementa una funcionalidad con un enfoque no obvio
- Se crea o actualiza un artefacto de Notion/Jira/GitHub con contenido relevante
- Se hace un cambio de configuración o se prepara el entorno

### Tras descubrimientos
- Un descubrimiento no obvio sobre el código
- Un gotcha, caso límite o comportamiento inesperado
- Se establece un patrón (nombres, estructura, convención)
- Se aprende una preferencia o restricción del usuario

### Tras una confirmación o rechazo del usuario
- El usuario confirma una recomendación tuya ("dale con eso", "hagámoslo así", "me parece bien", "de acuerdo", "perfecto", o el equivalente en su idioma)
- El usuario rechaza una opción o un enfoque ("no, mejor X", "ese no", o el equivalente en su idioma)
- El usuario expresa una preferencia ("prefiero X sobre Y", "siempre hazlo así", o el equivalente en su idioma)
- El usuario toma una decisión después de que presentaste opciones o trade-offs
- Una discusión concluye con una dirección clara elegida — aunque la haya propuesto el agente

### Autochequeo — pregúntate tras CADA tarea:
> "¿Acabo yo o el usuario de tomar una decisión, confirmar una recomendación, expresar una preferencia, arreglar un bug, aprender algo no obvio o establecer una convención? Si es así, llama a mem_save AHORA."

Formato para `mem_save`:
- **title**: Verbo + qué — corto y buscable (p. ej. "Arreglado N+1 en UserList", "Elegido Zustand sobre Redux"). El título es el ANCLA del asunto (guía la búsqueda y la detección de conflictos): hazlo específico.
- **type** — enruta por lo que ES la memoria:
  - `decision` — una decisión tomada (incluye restricciones y la orientación/misión del proyecto)
  - `preference` — un gusto o convención del usuario (idioma, estilo). Suele ser global
  - `architecture` — estructura del sistema y patrones/idiomas de código recurrentes
  - `bugfix` — la causa raíz de un bug resuelto
  - `discovery` — un hecho del sistema/entorno, aprendizaje o configuración (CATCH-ALL, por defecto)
- **scope**: `project` (def.) | `global`. Usa `global` para lo que aplica en TODOS tus proyectos (típico de `preference`); `project` para lo específico de este repo. (Ya no existe `personal`.)
- **resolution** (opcional): pon `open` cuando el usuario deje algo SIN resolver (aparecerá en la bandeja "Pendientes"). Para cerrarlo, guarda una memoria NUEVA que lo reemplace, en vez de editar la vieja.
- **project** y **session_id**: reutiliza los valores del bloque "[uso interno kandel]" inyectado en la conversación. El `session_id` liga la memoria a la charla que la originó (trazabilidad); si no aparece, omítelo.
- **content**:
  **What**: Una frase — qué se hizo
  **Why**: Qué lo motivó (petición del usuario, bug, rendimiento, etc.)
  **Where**: Archivos o rutas afectadas
  **Learned**: Gotchas, casos límite, cosas que te sorprendieron (omite si no hay)

### Reglas de actualización

- Temas distintos NO deben sobrescribirse entre sí (ejemplo: decisión de arquitectura vs bugfix)
- **Prefiere `mem_update` cuando ya conoces el ID de la memoria que quedó obsoleta** (la acabas de ver
  en el contexto o vía `mem_search`): edita en sitio en vez de guardar una nueva. Es lo más preciso,
  inmediato y limpio — reembebe, cuenta la revisión y deja UNA sola memoria, sin duplicados. Aplica a
  CUALQUIER tipo.
- **Si NO conoces la vieja (o la descubres tarde), guarda la nueva igual.** Para los HECHOS
  (`architecture` / `bugfix` / `discovery`) que cambian —config que se mueve, patrón que se abandona,
  estructura que se rehace— el barrido detecta el REEMPLAZO por sí solo y oculta el hecho caduco (el más
  reciente gana). Es una **red de seguridad automática**, no un sustituto: cuesta un juicio y conserva la
  vieja como histórico, así que `mem_update` sigue siendo el camino preferente cuando puedes usarlo.

## FILTRO DE CAPTURA (qué merece memoria)

Por defecto, guarda de más: el descarte lo hacen dos compuertas de juicio.

**Compuerta A — ¿entra?** ¿Es una decisión / convención / preferencia / restricción / bugfix con causa
raíz / descubrimiento no obvio / gotcha / patrón / confirmación o rechazo del usuario / dirección
concluida? Si NINGUNO aplica → no guardes.

**Compuerta B — ¿es trivial?** Descarta si CUALQUIERA es "sí":
1. ¿Ya se deriva del código, git o CLAUDE.md? (no dupliques el repo)
2. ¿Solo importa para esta conversación? (efímero)
3. ¿Es genérico/obvio, no específico de este proyecto?
4. ¿Le falta un "por qué" o contenido sustantivo?
Si B lo descartaría pero el usuario insiste, guarda SOLO el "por qué" no obvio.

## CUÁNDO BUSCAR EN LA MEMORIA

Las sesiones y memorias recientes YA se inyectan al iniciar la sesión (vía el hook), así que NO
necesitas llamar a `mem_context` solo para verlas. Recurre a las tools solo cuando el contexto
inyectado no baste. Cuando el usuario pida recordar algo — cualquier variación de "recuerda",
"qué hicimos", "cómo lo resolvimos", o el equivalente — o haga referencia a trabajo previo que no esté ya en el contexto:
1. Llama a `mem_search` con palabras clave relevantes (búsqueda semántica vectorial por significado)
2. Si encuentras una coincidencia, usa `mem_get` para el contenido completo sin truncar
3. Usa `mem_context` solo para volver a listar lo reciente si el contexto inyectado se truncó o se perdió

NO cargues ni llames estas tools en peticiones triviales que no necesitan memoria previa.

## AL ARRANCAR — actúa sobre el contexto inyectado (viene en bandejas)

El bloque de contexto que se te inyecta al iniciar la sesión viene organizado en bandejas. Cada una
pide una acción distinta:

- **Siempre presente (normativas)**: son las reglas vigentes del proyecto y del usuario. Respétalas
  durante toda la sesión (no hace falta re-buscarlas).
- **Pendientes por resolver**: cosas que quedaron abiertas. Al arrancar, revísalas y ofrece retomarlas.
- **Recientes**: es una ventana CHICA de hechos recientes (búsqueda perezosa). Si vas a trabajar en un
  tema y necesitas más contexto factual del que aparece ahí, llama a `mem_search` para traerlo.
- **Salud de memoria**: si aparecen avisos ("normativas sin verificar contra conflictos", "demasiadas
  normativas"), coméntaselos al usuario. Las normativas se obedecen igual; el aviso es por
  mantenimiento o por riesgo de contradicción aún no comprobada.

## PROTOCOLO DE CIERRE DE SESIÓN (obligatorio)

Antes de terminar una sesión o decir "listo" / "ya está", DEBES:
1. Llamar a `mem_session_summary` con esta estructura:

## Objetivo
[En qué estábamos trabajando esta sesión]

## Instrucciones
[Preferencias o restricciones del usuario descubiertas — omite si no hay]

## Descubrimientos
- [Hallazgos técnicos, gotchas, aprendizajes no obvios]

## Logrado
- [Lo completado con sus detalles clave]

## Próximos pasos
- [Lo que falta por hacer — para la próxima sesión]

## Archivos relevantes
- ruta/al/archivo — [qué hace o qué cambió]

Esto NO es opcional. Si lo omites, la próxima sesión empieza a ciegas.

## TRAS UNA COMPACTACIÓN

Si ves un mensaje sobre compactación o reinicio de contexto:
1. INMEDIATAMENTE llama a `mem_session_summary` con el contenido del resumen compactado — esto persiste lo que se hizo antes de la compactación
2. Luego llama a `mem_context` para recuperar cualquier contexto adicional de sesiones anteriores
3. Solo ENTONCES continúa trabajando

No omitas el paso 1. Sin él, todo lo hecho antes de la compactación se pierde de la memoria.
