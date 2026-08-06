package hook

// protocolSessionStart es el bloque que el hook SessionStart imprime por stdout; el agente lo
// inyecta como contexto adicional. Recuerda al modelo el protocolo de memoria persistente.
//
// Es deliberadamente CORTO. Este texto se paga en CADA sesión, se use la memoria o no, así que solo
// puede contener lo que hace falta antes de tocar ninguna tool: cómo cargarlas, cuándo disparar el
// guardado, y qué hacer con el contexto que viene debajo. Todo lo que sea "cómo se usa esta tool"
// —qué guardar, cómo clasificar el tipo, las reglas de la bitácora— vive en la DESCRIPCIÓN de cada
// tool (internal/port_in/mcp/tools_memory.go), que el agente solo carga cuando va a usarla. Antes de
// añadir nada aquí, comprueba que no pueda ir allí.
const protocolSessionStart = `## Memoria persistente kandel — PROTOCOLO ACTIVO

Tienes memoria persistente entre sesiones (servidor MCP: kandel). Es OBLIGATORIA, no opcional.

### Carga las tools antes de llamarlas
Los esquemas mem_* están DIFERIDOS: llamar una sin cargarla falla. Una sola llamada, la primera vez
que la memoria sea relevante en la sesión:
  ToolSearch select:mcp__kandel__mem_save,mcp__kandel__mem_search,mcp__kandel__mem_context,mcp__kandel__mem_get,mcp__kandel__mem_save_prompt

Las demás (mem_update, mem_link, mem_thread, mem_pending, mem_compare, mem_judge, mem_delete,
mem_restore) cárgalas cuando las necesites. Cada tool explica en su descripción cuándo y cómo usarla.

### Guarda en el momento en que ocurre, no al final
Después de CADA tarea pregúntate: "¿se acaba de tomar una decisión, confirmar una recomendación,
expresar una preferencia, arreglar un bug, descubrir algo no obvio o fijar una convención?" Si la
respuesta es sí → ` + "`mem_save`" + ` AHORA, sin esperar a que el usuario lo pida.

Al guardar en scope=project (incluida la bitácora) pasa SIEMPRE el project y el session_id del bloque
"[uso interno kandel]"; sin project el guardado falla con "proyecto ambiguo".

### Revisa antes de cerrar
Al terminar el trabajo, repasa lo que guardaste hoy: ¿alguna memoria quedó equivocada, incompleta o
caducada por lo que pasó después? Corrígela con mem_update, o guarda la versión buena pasando
supersedes con el id de la vieja. Guardar proactivamente sin este repaso acumula memorias falsas, y
una memoria falsa cuesta más que la que nunca se guardó.

### Qué hacer con el contexto de abajo
- "Bitácora — estado actual" es dónde nos quedamos: ofrécele al usuario retomarlo. Ahí solo van
  títulos; pide el cuerpo con mem_get <id> y el hilo completo con mem_thread <id>.
- Si hay avisos en "Salud de memoria", coméntaselos al usuario.
- Las memorias relevantes a cada petición se inyectan SOLAS por similitud con lo que el usuario pide;
  no llames a mem_context para eso. mem_search es para ir más allá de lo inyectado.

El REGISTRO de la sesión se cierra solo (hook determinista): no hay ninguna tool de "cerrar sesión"
que llamar. Eso no te exime del repaso de arriba, que es sobre el contenido, no sobre el registro.`

// firstMessageContext es el additionalContext que el hook UserPromptSubmit inyecta SOLO en el
// primer mensaje de la sesión. Recordatorio CONDICIONAL (no una orden incondicional): el contexto
// reciente ya se inyecta en el session-start, así que NO hace falta llamar mem_context; solo se
// indica cómo cargar las tools (diferidas) y cuándo usarlas. Lo ve el modelo, no el usuario.
const firstMessageContext = "Tienes herramientas de memoria kandel (servidor MCP: kandel), pero sus esquemas están diferidos. " +
	"El contexto reciente del proyecto YA se inyecta al iniciar la sesión, así que NO necesitas llamar a mem_context. " +
	"CUANDO tengas algo que valga la pena persistir (una decisión, un arreglo de bug, una convención, un descubrimiento, una preferencia del usuario) " +
	"o necesites recordar algo específico que no esté ya en el contexto, PRIMERO carga las tools con: " +
	"ToolSearch select:mcp__kandel__mem_save,mcp__kandel__mem_search,mcp__kandel__mem_context," +
	"mcp__kandel__mem_get,mcp__kandel__mem_save_prompt — luego llama la tool que necesites. " +
	"NO llames estas tools en peticiones triviales que no crean nada que valga la pena recordar."

// postCompactionHeader es la primera parte del bloque que imprime el hook post-compactación. El
// resto (los pasos numerados con el nombre del proyecto interpolado) se compone en el handler.
const postCompactionHeader = `## Memoria persistente kandel — PROTOCOLO ACTIVO (post-compactación)

INSTRUCCIÓN CRÍTICA POST-COMPACTACIÓN — sigue estos pasos EN ORDEN:`

// postCompactionFooter cierra el bloque post-compactación tras los pasos interpolados.
const postCompactionFooter = `2. Si necesitas más detalle sobre un tema concreto, llama a mem_search con palabras clave relevantes.
3. Solo ENTONCES continúa con lo que pidió el usuario.

El contexto reciente ya viene re-inyectado abajo. Estos pasos recuperan el resto; sin ellos empiezas a ciegas.`
