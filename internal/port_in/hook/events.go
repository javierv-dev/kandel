package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// sessionStart maneja el hook SessionStart: asegura el daemon, migra el nombre del proyecto si
// cambió, crea la sesión, importa chunks sincronizados y por último imprime el protocolo + el
// contexto del proyecto para que el agente lo inyecte.
func (r *runner) sessionStart(p payload) error {
	project := detectProject(p.CWD)
	old := oldProjectName(p.CWD)
	r.klog(fmt.Sprintf("inicio: session_id=%s project=%s cwd=%s", orQ(p.SessionID), orQ(project), orQ(p.CWD)))

	if err := r.client.ensureDaemon(r.ctx, r); err != nil {
		// Sin daemon no hay backend; aun así se imprime el protocolo (es útil al modelo).
		fmt.Fprint(r.out, protocolSessionStart)
		return nil
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

	// Importa los chunks sincronizados por git (en segundo plano, sin bloquear).
	if p.CWD != "" {
		if _, err := os.Stat(filepath.Join(p.CWD, ".kandel", "manifest.json")); err == nil {
			spawnSyncImport(p.CWD)
		}
	}

	// Precalienta el modelo de embeddings (asíncrono en el daemon): mata el arranque en frío de
	// Ollama (~7s) para que el primer Save real de la sesión responda rápido.
	code, _ := r.client.warmup(r.ctx)
	r.klog("POST /embeddings/warmup -> HTTP " + code)

	// Inyecta el protocolo + el contexto del proyecto.
	fmt.Fprint(r.out, protocolSessionStart)
	if text := r.client.fetchContext(r.ctx, project); text != "" {
		fmt.Fprintf(r.out, "\n%s\n", text)
	}
	return nil
}

// postCompaction maneja el hook tras una compactación: asegura el daemon y la sesión, e inyecta un
// protocolo que ordena persistir el resumen compactado y recuperar el contexto antes de seguir.
func (r *runner) postCompaction(p payload) error {
	project := detectProject(p.CWD)
	r.klog(fmt.Sprintf("inicio: session_id=%s project=%s", orQ(p.SessionID), orQ(project)))

	if err := r.client.ensureDaemon(r.ctx, r); err != nil {
		r.printPostCompaction(project, "")
		return nil
	}

	if p.SessionID != "" && project != "" {
		code, _ := r.client.postSession(r.ctx, p.SessionID, project, p.CWD)
		r.klog("POST /sessions -> HTTP " + code)
	}

	r.printPostCompaction(project, r.client.fetchContext(r.ctx, project))
	return nil
}

// printPostCompaction compone e imprime el bloque post-compactación con el nombre del proyecto
// interpolado y, opcionalmente, el contexto recuperado.
func (r *runner) printPostCompaction(project, context string) {
	fmt.Fprintln(r.out, postCompactionHeader)
	fmt.Fprintf(r.out, "\n1. PRIMERO: Llama a mem_session_summary con el contenido del resumen compactado de arriba. Usa project: '%s'.\n", project)
	fmt.Fprint(r.out, "   Esto preserva lo que se logró antes de la compactación.\n\n")
	fmt.Fprintf(r.out, "2. LUEGO: Llama a mem_context con project: '%s' para recuperar el historial reciente de sesiones y memorias.\n", project)
	fmt.Fprintln(r.out, postCompactionFooter)
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
	return nil
}

// subagentStop maneja el hook SubagentStop: envía la salida del subagente a la captura pasiva, que
// extrae aprendizajes en el servidor. Si no hay salida, no hace nada.
func (r *runner) subagentStop(p payload) error {
	if p.Stdout == "" {
		return nil
	}
	project := detectProject(p.CWD)
	code, _ := r.client.passiveCapture(r.ctx, p.SessionID, p.Stdout, project, "subagent-stop")
	r.klog(fmt.Sprintf("POST /memories/passive project=%s -> HTTP %s", project, code))
	return nil
}
