package hook

import (
	"encoding/json"
	"fmt"

	"github.com/javierv-dev/kandel/internal/config"
)

// sessionStart maneja el hook SessionStart: asegura el daemon, migra el nombre del proyecto si
// cambió, crea la sesión, dispara el pull del hub (si el auto-sync está activo) y por último imprime
// el protocolo + el contexto del proyecto para que el agente lo inyecte.
func (r *runner) sessionStart(p payload) error {
	project := detectProject(p.CWD)
	old := oldProjectName(p.CWD)
	r.klog(fmt.Sprintf("inicio: session_id=%s project=%s cwd=%s", orQ(p.SessionID), orQ(project), orQ(p.CWD)))

	ctxText, _ := r.prepareStartup(project, old, p)

	// Inyecta los banners (desalineación / versión nueva, si aplican) + el protocolo + los metadatos
	// internos (project/session_id) + el contexto (vacío si el daemon estaba caído). El protocolo se
	// imprime siempre, aunque no haya backend, porque es útil al modelo.
	fmt.Fprint(r.out, r.assetMismatchBanner())
	fmt.Fprint(r.out, r.updateBanner())
	fmt.Fprint(r.out, protocolSessionStart)
	r.injectMetadata(project, p.SessionID)
	if ctxText != "" {
		fmt.Fprintf(r.out, "\n%s\n", ctxText)
	}
	return nil
}

// prepareStartup corre los efectos de arranque de sesión —asegura el daemon, migra el nombre del
// proyecto si cambió, crea/reactiva la sesión, dispara el pull del hub y precalienta el embebedor— y
// devuelve el contexto del proyecto. daemonUp es false si el daemon no respondió (en ese
// caso ctxText es "" y no se hizo ningún efecto). Lo comparten el arranque de Claude Code
// (sessionStart) y el de Antigravity (agPreInvocation), que solo difieren en cómo formatean la salida.
func (r *runner) prepareStartup(project, old string, p payload) (ctxText string, daemonUp bool) {
	if err := r.client.ensureDaemon(r.ctx, r); err != nil {
		return "", false
	}

	// Migra el nombre del proyecto si cambió (idempotente en el servidor).
	if old != project && old != "" && project != "" {
		_, _ = r.client.migrateProject(r.ctx, old, project)
	}

	// Crea la sesión (se registra el código HTTP para diagnosticar fallos).
	if p.SessionID != "" && project != "" {
		code, _ := r.client.postSession(r.ctx, p.SessionID, project, p.CWD)
		r.klog("POST /sessions -> HTTP " + code)
	} else {
		r.klog("no se crea sesión: session_id o project vacíos")
	}

	// Baja lo ajeno del hub (asíncrono en el daemon; responde 202) si los disparadores automáticos
	// están activos. No bloquea el arranque: un arranque en frío puede traer mucho estado, así que se
	// delega al daemon. Lo recién bajado entra al vuelo (puede no verse en el contexto de ESTA sesión,
	// pero sí en la siguiente y lo cubre el timer).
	if config.AutoSyncEnabled() {
		code, _ := r.client.triggerSyncPull(r.ctx)
		r.klog("POST /sync/pull -> HTTP " + code)
	}

	// Precalienta el modelo de embeddings (asíncrono en el daemon): mata el arranque en frío de
	// Ollama (~7s) para que el primer Save real de la sesión responda rápido.
	code, _ := r.client.warmup(r.ctx)
	r.klog("POST /embeddings/warmup -> HTTP " + code)

	return r.client.fetchContext(r.ctx, project), true
}

// injectMetadata escribe el bloque de metadatos internos de la conversación (project, session_id, …)
// en el stdout del hook, con una línea de separación. No imprime nada si no hay datos que inyectar.
func (r *runner) injectMetadata(project, sessionID string) {
	if block := (agentMetadata{project: project, sessionID: sessionID}).render(); block != "" {
		fmt.Fprintf(r.out, "\n%s", block)
	}
}

// assetMismatchBanner devuelve un aviso para el agente si la versión de los assets instalados no
// coincide con la del binario en ejecución (§21 Eje 3): la integración quedó vieja tras actualizar
// kandel sin reinstalar. Devuelve "" si coinciden, no hay marca de instalación, o no se puede
// determinar (nunca molesta de más). El texto es deliberadamente general y no técnico (sin números
// de versión ni jerga assets/binario). Espeja diagnostic.InstalledAssetsCheck, pero proactivo en
// cada arranque de sesión.
func (r *runner) assetMismatchBanner() string {
	if r.installedVersion == nil {
		return ""
	}
	installed, present, err := r.installedVersion()
	if err != nil || !present || installed == r.running {
		return ""
	}
	return "⚠️ kandel desactualizado — reinstala con `kandel install claude-code` y reinicia el agente.\n\n"
}

// updateBanner devuelve un aviso para el agente si hay una versión de kandel más nueva publicada
// (§21 Eje 6). Devuelve "" si no hay checker, no se pudo determinar (offline/desactivado) o ya está
// al día. Mensaje general y no técnico, coherente con el banner de desalineación.
func (r *runner) updateBanner() string {
	if r.updates == nil {
		return ""
	}
	latest, newer := r.updates.Newer(r.ctx, r.running)
	if !newer {
		return ""
	}
	return fmt.Sprintf("⚠️ Hay una versión nueva de kandel (%s) — descárgala y reinstala con `kandel install claude-code`.\n\n", latest)
}

// postCompaction maneja el hook tras una compactación: asegura el daemon y la sesión, e inyecta un
// protocolo que ordena persistir el resumen compactado y recuperar el contexto antes de seguir.
func (r *runner) postCompaction(p payload) error {
	project := detectProject(p.CWD)
	r.klog(fmt.Sprintf("inicio: session_id=%s project=%s", orQ(p.SessionID), orQ(project)))

	if err := r.client.ensureDaemon(r.ctx, r); err != nil {
		r.printPostCompaction(project, p.SessionID, "")
		return nil
	}

	if p.SessionID != "" && project != "" {
		code, _ := r.client.postSession(r.ctx, p.SessionID, project, p.CWD)
		r.klog("POST /sessions -> HTTP " + code)
	}

	r.printPostCompaction(project, p.SessionID, r.client.fetchContext(r.ctx, project))
	return nil
}

// printPostCompaction compone e imprime el bloque post-compactación con el nombre del proyecto
// interpolado, los metadatos internos (la compactación borró el contexto previo, así que hay que
// re-inyectarlos) y, opcionalmente, el contexto recuperado.
func (r *runner) printPostCompaction(project, sessionID, context string) {
	fmt.Fprintln(r.out, postCompactionHeader)
	fmt.Fprintf(r.out, "\n1. Llama a mem_context con project: '%s' para recuperar el historial reciente (bitácora y memorias).\n", project)
	fmt.Fprintln(r.out, postCompactionFooter)
	r.injectMetadata(project, sessionID)
	if context != "" {
		fmt.Fprintf(r.out, "\n%s\n", context)
	}
}

// userPromptSubmit maneja el hook UserPromptSubmit: persiste el prompt y, en el primer mensaje de
// la sesión, inyecta la instrucción de cargar las tools mem_* (diferidas). Siempre imprime JSON
// válido por stdout (si no, el agente bloquea el mensaje).
func (r *runner) userPromptSubmit(p payload) error {
	if p.Prompt != "" && p.SessionID != "" {
		code, _ := r.client.savePrompt(r.ctx, p.SessionID, p.Prompt)
		r.klog(fmt.Sprintf("POST /prompts session_id=%s -> HTTP %s", p.SessionID, code))
	} else {
		r.klog("no se persiste prompt: session_id o prompt vacíos")
	}

	// Dispara un backfill inmediato (asíncrono en el daemon) de las memorias que se hayan guardado
	// sin vector. Es barato si no hay pendientes (una consulta que vuelve vacía) y deja los vectores
	// al día sin esperar al barrido periódico. No bloquea: el daemon responde 202.
	bfCode, _ := r.client.triggerBackfill(r.ctx)
	r.klog("POST /embeddings/backfill -> HTTP " + bfCode)

	if firstMessage(p.SessionID) {
		out := map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":     "UserPromptSubmit",
				"additionalContext": firstMessageContext,
			},
		}
		raw, _ := json.Marshal(out)
		fmt.Fprintln(r.out, string(raw))
		return nil
	}
	fmt.Fprintln(r.out, "{}")
	return nil
}

// sessionStop maneja el hook Stop: marca la sesión como cerrada vía la API.
func (r *runner) sessionStop(p payload) error {
	if p.SessionID == "" {
		return nil
	}
	code, _ := r.client.endSession(r.ctx, p.SessionID)
	r.klog(fmt.Sprintf("POST /sessions/%s/end -> HTTP %s", p.SessionID, code))

	// Sube lo local al hub (asíncrono en el daemon; responde 202) si los disparadores automáticos
	// están activos. NO corre `kandel sync` síncrono: podría tardar por red y colgar la terminal al
	// cerrar (§13.1). El daemon hace el push de fondo; lo no enviado se reintenta (la cola es la verdad).
	if config.AutoSyncEnabled() {
		syncCode, _ := r.client.triggerSyncPush(r.ctx)
		r.klog("POST /sync/push -> HTTP " + syncCode)
	}
	return nil
}
