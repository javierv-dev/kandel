package hook

import (
	"encoding/json"
	"fmt"
	"strings"
)

// protocolOcSessionStart es el bloque de protocolo para OpenCode v2. Las tools MCP del servidor
// kandel quedan disponibles directamente en el catálogo de OpenCode (sin ToolSearch), mientras que
// el plugin de ciclo de vida inyecta arranque, Capa 2 por turno y post-compactación en el contexto.
const protocolOcSessionStart = `## Memoria persistente kandel — PROTOCOLO ACTIVO

Tienes memoria persistente entre sesiones (servidor MCP: kandel). Es OBLIGATORIA, no opcional.
El almacén es COMPARTIDO con cualquier otro agente que use kandel en este proyecto (Claude Code, Antigravity, Pi, OpenCode).

### Herramientas disponibles
Tools principales del servidor MCP kandel: ` + "`mem_save`" + `, ` + "`mem_search`" + `, ` + "`mem_context`" + `, ` + "`mem_get`" + `, ` + "`mem_update`" + `, ` + "`mem_handoff_get`" + `, ` + "`mem_handoff_save`" + `, ` + "`mem_handoff_resume`" + `.
Tools adicionales cuando las necesites: ` + "`mem_link`" + `, ` + "`mem_thread`" + `, ` + "`mem_pending`" + `, ` + "`mem_compare`" + `, ` + "`mem_judge`" + `, ` + "`mem_delete`" + `, ` + "`mem_restore`" + `, ` + "`mem_doctor`" + `, ` + "`mem_sync`" + `.

### Guarda en el momento en que ocurre, no al final
Después de CADA tarea pregúntate: "¿se acaba de tomar una decisión, confirmar una recomendación,
expresar una preferencia, arreglar un bug, descubrir algo no obvio o fijar una convención?" Si la
respuesta es sí → ` + "`mem_save`" + ` AHORA, sin esperar a que el usuario lo pida.

Al guardar en scope=project (incluida la bitácora) pasa SIEMPRE el project y el session_id del bloque
"[uso interno kandel]"; sin project el guardado falla con "proyecto ambiguo".
Si una memoria que conoces quedó obsoleta, actualiza con ` + "`mem_update`" + ` (o declara ` + "`supersedes`" + `).

### Relevo entre sesiones y agentes
Para una tarea que deba sobrevivir a un corte o continuar en otro agente, guarda un checkpoint breve con
` + "`mem_handoff_save`" + ` usando el session_id actual. Al retomar, lista con ` + "`mem_handoff_get`" + ` y vincula la versión
elegida con ` + "`mem_handoff_resume`" + `. Contrasta siempre el reporte con Git y las pruebas.

### Qué hacer con el contexto de abajo
- "Bitácora — estado actual" es dónde nos quedamos: ofrécele al usuario retomarlo. Ahí solo van
  títulos; pide el cuerpo con ` + "`mem_get`" + ` y el hilo completo con ` + "`mem_thread`" + `.
- Si hay avisos en "Salud de memoria", coméntaselos al usuario.
- Las memorias relevantes a cada petición se inyectan SOLAS por similitud con lo que el usuario pide;
  no llames a ` + "`mem_context`" + ` para eso. ` + "`mem_search`" + ` es para ir más allá de lo inyectado.`

// ocSessionStart maneja el arranque de sesión en OpenCode v2: asegura el daemon, crea la sesión
// opencode:<id>, lanza sync/pull + warmup e imprime los banners (assets, release, discrepancia de
// proyecto y alertas del juez), el protocolo, los metadatos y el contexto inicial.
func (r *runner) ocSessionStart(p payload) error {
	project := detectProject(p.CWD)
	dirName := rootDirProjectName(p.CWD)
	r.klog(fmt.Sprintf("oc-inicio: session_id=%s project=%s dir=%s cwd=%s", orQ(p.SessionID), orQ(project), orQ(dirName), orQ(p.CWD)))

	ctxText, _ := r.prepareStartup(project, p)

	fmt.Fprint(r.out, r.assetMismatchBannerFor("opencode"))
	fmt.Fprint(r.out, r.updateBannerFor("opencode"))
	fmt.Fprint(r.out, r.projectMismatchBanner(project, dirName))
	fmt.Fprint(r.out, r.systemAlertsBanner())
	fmt.Fprint(r.out, protocolOcSessionStart)
	r.injectMetadata(project, p.SessionID)
	if ctxText != "" {
		fmt.Fprintf(r.out, "\n%s\n", ctxText)
	}
	return nil
}

// ocPrompt maneja cada turno de usuario en OpenCode v2 (Capa 2): asegura la sesión en el primer
// mensaje, persiste el prompt, dispara el backfill de embeddings e imprime JSON `{"additionalContext": ...}`
// (o `{}` si no hay coincidencias relevantes).
func (r *runner) ocPrompt(p payload) error {
	project := detectProject(p.CWD)

	if firstMessage(p.SessionID) {
		r.ensureSession(p, project)
	}

	if p.Prompt != "" && p.SessionID != "" {
		code, err := r.client.savePrompt(r.ctx, p.SessionID, p.Prompt)
		r.klogResultado("POST /prompts session_id="+p.SessionID, code, err)
	} else {
		r.klog("no se persiste prompt: session_id o prompt vacíos")
	}

	bfCode, bfErr := r.client.triggerBackfill(r.ctx)
	r.klogResultado("POST /embeddings/backfill", bfCode, bfErr)

	relText, injected := "", []int64(nil)
	if p.Prompt != "" {
		excludeSession := p.SessionID
		if wasCompacted(p.SessionID) {
			excludeSession = ""
		}
		relText, injected = r.client.fetchRelevant(r.ctx, project, p.Prompt, loadInjectedIDs(p.SessionID), excludeSession)
		recordInjectedIDs(p.SessionID, injected)
	}

	relText = strings.TrimSpace(relText)
	if relText == "" {
		fmt.Fprintln(r.out, "{}")
		return nil
	}
	raw, _ := json.Marshal(map[string]any{
		"additionalContext": relText,
	})
	fmt.Fprintln(r.out, string(raw))
	return nil
}
