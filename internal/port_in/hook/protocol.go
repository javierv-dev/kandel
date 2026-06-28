package hook

// protocolSessionStart es el bloque que el hook SessionStart imprime por stdout; el agente lo
// inyecta como contexto adicional. Recuerda al modelo el protocolo de memoria persistente.
const protocolSessionStart = `## Kandel Persistent Memory — ACTIVE PROTOCOL

You have kandel memory tools (MCP server: kandel). This protocol is MANDATORY and ALWAYS ACTIVE.

### TOOLS ARE DEFERRED — you MUST load them with ToolSearch before calling any mem_* tool
The mem_* tool schemas are NOT loaded by default. Calling one without loading fails. Load the core
set with a SINGLE ToolSearch (do it the first time memory is relevant in the session):
  ToolSearch select:mcp__kandel__mem_save,mcp__kandel__mem_search,mcp__kandel__mem_context,mcp__kandel__mem_session_summary,mcp__kandel__mem_get,mcp__kandel__mem_save_prompt

Other tools (load with ToolSearch when needed): mem_update, mem_suggest_topic_key, mem_session_start, mem_session_end, mem_capture_passive, mem_pin, mem_unpin, mem_review, mem_compare, mem_judge, mem_delete (logical/reversible), mem_restore

### PROACTIVE SAVE — do NOT wait for the user to ask
Call ` + "`mem_save`" + ` IMMEDIATELY after ANY of these:
- Decision made (architecture, convention, workflow, tool choice)
- Bug fixed (include root cause)
- Convention or workflow documented/updated
- Non-obvious discovery, gotcha, or edge case found
- Pattern established (naming, structure, approach)
- User preference or constraint learned
- User confirms your recommendation or rejects an approach
- A discussion concludes with a clear direction chosen

**Self-check after EVERY task**: "Did I or the user just make a decision, confirm a recommendation, express a preference, fix a bug, learn something, or establish a convention? If yes → mem_save NOW."

### SEARCH MEMORY when (only when it actually helps — not for trivial requests):
Recent sessions and memories are ALREADY injected below at session start, so you do NOT need to
call mem_context just to see them. Use mem_search for DEEPER or SPECIFIC recall:
- The user asks to recall something ("remember", "what did we do", or the equivalent in their language)
- You are about to work on something that may have been done before and the injected context is not enough

### SESSION CLOSE — before saying "done":
Call ` + "`mem_session_summary`" + ` with: Goal, Discoveries, Accomplished, Next Steps, Relevant Files.`

// firstMessageContext es el additionalContext que el hook UserPromptSubmit inyecta SOLO en el
// primer mensaje de la sesión. Recordatorio CONDICIONAL (no una orden incondicional): el contexto
// reciente ya se inyecta en el session-start, así que NO hace falta llamar mem_context; solo se
// indica cómo cargar las tools (diferidas) y cuándo usarlas. Lo ve el modelo, no el usuario.
const firstMessageContext = "kandel memory tools (MCP server: kandel) are available but their schemas are deferred. " +
	"Recent project context is ALREADY injected at session start, so you do NOT need to call mem_context. " +
	"WHEN you have something worth persisting (a decision, bug fix, convention, discovery, user preference) " +
	"or need to recall something specific that is not already in context, FIRST load the tools with: " +
	"ToolSearch select:mcp__kandel__mem_save,mcp__kandel__mem_search,mcp__kandel__mem_context," +
	"mcp__kandel__mem_get,mcp__kandel__mem_save_prompt — then call the tool you need. " +
	"Do NOT call these tools for trivial requests that create nothing worth remembering."

// postCompactionHeader es la primera parte del bloque que imprime el hook post-compactación. El
// resto (los pasos numerados con el nombre del proyecto interpolado) se compone en el handler.
const postCompactionHeader = `## Kandel Persistent Memory — ACTIVE PROTOCOL (post-compaction)

CRITICAL INSTRUCTION POST-COMPACTION — follow these steps IN ORDER:`

// postCompactionFooter cierra el bloque post-compactación tras los pasos interpolados.
const postCompactionFooter = `3. If you need more detail on a specific topic, call mem_search with relevant keywords.
4. Only THEN continue working on what the user asked.

All steps are MANDATORY. Without them, you lose context and start blind.`
