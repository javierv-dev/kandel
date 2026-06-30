package hook

// protocolSessionStart es el bloque que el hook SessionStart imprime por stdout; el agente lo
// inyecta como contexto adicional. Recuerda al modelo el protocolo de memoria persistente.
const protocolSessionStart = `## Memoria persistente kandel — PROTOCOLO ACTIVO

Tienes herramientas de memoria kandel (servidor MCP: kandel). Este protocolo es OBLIGATORIO y SIEMPRE ACTIVO.

### LAS TOOLS ESTÁN DIFERIDAS — DEBES cargarlas con ToolSearch antes de llamar cualquier tool mem_*
Los esquemas de las tools mem_* NO se cargan por defecto: llamar una sin cargarla falla. Carga el
conjunto principal con UN SOLO ToolSearch (hazlo la primera vez que la memoria sea relevante en la sesión):
  ToolSearch select:mcp__kandel__mem_save,mcp__kandel__mem_search,mcp__kandel__mem_context,mcp__kandel__mem_session_summary,mcp__kandel__mem_get,mcp__kandel__mem_save_prompt

Otras tools (cárgalas con ToolSearch cuando las necesites): mem_update, mem_session_start, mem_session_end, mem_capture_passive, mem_pending, mem_compare, mem_judge, mem_delete (lógico/reversible), mem_restore

### GUARDADO PROACTIVO — no esperes a que el usuario lo pida
Llama a ` + "`mem_save`" + ` INMEDIATAMENTE después de CUALQUIERA de estos:
- Se toma una decisión (arquitectura, convención, flujo de trabajo, elección de herramienta)
- Se arregla un bug (incluye la causa raíz)
- Se documenta o actualiza una convención o un flujo de trabajo
- Se descubre algo no obvio, un gotcha o un caso límite
- Se establece un patrón (nombres, estructura, enfoque)
- Se aprende una preferencia o restricción del usuario
- El usuario confirma tu recomendación o rechaza un enfoque
- Una discusión concluye con una dirección clara elegida

**Autochequeo después de CADA tarea**: "¿Acabo yo o el usuario de tomar una decisión, confirmar una recomendación, expresar una preferencia, arreglar un bug, aprender algo o establecer una convención? Si es así → mem_save AHORA."

### BUSCA EN LA MEMORIA cuando (solo cuando de verdad ayude — no en peticiones triviales):
Las sesiones y memorias recientes YA se inyectan más abajo al iniciar la sesión, así que NO necesitas
llamar a mem_context solo para verlas. Usa mem_search para un recall MÁS PROFUNDO o ESPECÍFICO:
- El usuario pide recordar algo ("recuerda", "qué hicimos", o el equivalente en su idioma)
- Vas a trabajar en algo que quizá ya se hizo antes y el contexto inyectado no basta

### CIERRE DE SESIÓN — antes de decir "listo":
Llama a ` + "`mem_session_summary`" + ` con: Objetivo, Descubrimientos, Logrado, Próximos pasos, Archivos relevantes.`

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
const postCompactionFooter = `3. Si necesitas más detalle sobre un tema concreto, llama a mem_search con palabras clave relevantes.
4. Solo ENTONCES continúa con lo que pidió el usuario.

Todos los pasos son OBLIGATORIOS. Sin ellos, pierdes el contexto y empiezas a ciegas.`
