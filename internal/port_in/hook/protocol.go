package hook

// protocolSessionStart es el bloque que el hook SessionStart imprime por stdout; el agente lo
// inyecta como contexto adicional. Recuerda al modelo el protocolo de memoria persistente.
const protocolSessionStart = `## Memoria persistente kandel — PROTOCOLO ACTIVO

Tienes herramientas de memoria kandel (servidor MCP: kandel). Este protocolo es OBLIGATORIO y SIEMPRE ACTIVO.

### LAS TOOLS ESTÁN DIFERIDAS — DEBES cargarlas con ToolSearch antes de llamar cualquier tool mem_*
Los esquemas de las tools mem_* NO se cargan por defecto: llamar una sin cargarla falla. Carga el
conjunto principal con UN SOLO ToolSearch (hazlo la primera vez que la memoria sea relevante en la sesión):
  ToolSearch select:mcp__kandel__mem_save,mcp__kandel__mem_search,mcp__kandel__mem_context,mcp__kandel__mem_get,mcp__kandel__mem_save_prompt

Otras tools (cárgalas con ToolSearch cuando las necesites): mem_update, mem_pending, mem_compare, mem_judge, mem_delete (lógico/reversible), mem_restore

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

Al guardar, enruta el type por lo que ES: decisión→decision, gusto/convención del usuario→preference (suele global), patrón/idioma de código o estructura→architecture, causa raíz de bug→bugfix, hecho/aprendizaje/config→discovery (default), narrativa/avance del desarrollo→journal (la bitácora). scope=global solo si aplica a TODOS tus proyectos; si no, project. Marca resolution=open si el usuario deja algo sin resolver. En scope=project (incluido journal) PASA SIEMPRE el project explícito = el "Proyecto actual" que aparece más abajo en este contexto inyectado (o el del bloque "[uso interno kandel]"); si lo omites, el guardado falla con "proyecto ambiguo". Pasa también el session_id del bloque "[uso interno kandel]" para ligar la memoria a la charla que la originó.

BITÁCORA (type=journal): registra el avance del desarrollo por hito (fecha + qué + por qué). Es un log APPEND-ONLY exento del filtro de trivialidad (aunque sea cronológico/efímero, SÍ se guarda). Es scope=project: pasa SIEMPRE el project explícito (el "Proyecto actual" de arriba), o el guardado falla con "proyecto ambiguo". Mientras el tema está en vuelo, la entrada va con resolution=open (es el "estado actual", editable con mem_update); cuando el tema se cierra, pásala a resolution=resolved. Si una decisión de la bitácora restringe el trabajo futuro, guárdala ADEMÁS como type=decision (regla vigente).

### BUSCA EN LA MEMORIA cuando (solo cuando de verdad ayude — no en peticiones triviales):
Al arrancar solo se inyecta la CONTINUIDAD mínima (bitácora "estado actual" en títulos + salud de
memoria). Las memorias RELEVANTES a tu tarea se inyectan AUTOMÁTICAMENTE con cada mensaje del usuario
(por similitud con lo que pide), así que NO necesitas mem_context para eso. Usa las tools para ir más allá:
- mem_get <id> para el CUERPO de una entrada de la bitácora "estado actual" (arriba solo va el título).
- mem_search para un recall MÁS PROFUNDO o ESPECÍFICO: el usuario pide recordar algo ("recuerda",
  "qué hicimos", o el equivalente en su idioma), o vas a tocar algo que quizá ya se hizo y lo inyectado no basta.

### ACTÚA SOBRE EL CONTEXTO DE ABAJO:
"Bitácora — estado actual" es dónde nos quedamos: ofrécele al usuario retomarlo; pide el cuerpo con
mem_get <id> si lo necesitas. Si hay avisos en "Salud de memoria" (normativas sin verificar /
demasiadas / conflictos), coméntaselos al usuario.

La sesión se cierra sola (hook determinista); no necesitas llamar a ninguna tool al terminar. Guarda
lo que valga la pena con ` + "`mem_save`" + ` en el momento en que ocurre, no al final.`

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
