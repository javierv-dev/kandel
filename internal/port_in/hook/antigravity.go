package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// agInput son los campos del stdin JSON de Antigravity CLI que kandel usa. El contrato difiere del de
// Claude Code (ver payload): la sesión es conversationId, el directorio de trabajo es el primer
// workspacePath, y el prompt del usuario NO viene directo (habría que leerlo del transcript, que hoy no
// se toca — ver agPreInvocation). invocationNum distingue el arranque (0) de los turnos siguientes.
type agInput struct {
	ConversationID string   `json:"conversationId"`
	WorkspacePaths []string `json:"workspacePaths"`
	TranscriptPath string   `json:"transcriptPath"`
	InvocationNum  int      `json:"invocationNum"`
}

// parseAgInput lee y decodifica el JSON de entrada de Antigravity. Es tolerante igual que parsePayload:
// ante un cuerpo vacío o malformado devuelve un input en blanco en vez de fallar (un hook nunca debe
// romper el flujo del agente).
func parseAgInput(r io.Reader) agInput {
	var in agInput
	data, err := io.ReadAll(r)
	if err != nil || len(data) == 0 {
		return in
	}
	_ = json.Unmarshal(data, &in)
	return in
}

// cwd devuelve el directorio de trabajo (el primer workspacePath), o "" si no hay ninguno.
func (in agInput) cwd() string {
	if len(in.WorkspacePaths) > 0 {
		return in.WorkspacePaths[0]
	}
	return ""
}

// agStartHeader encabeza el contexto que se inyecta en el arranque (PreInvocation#0). El protocolo
// COMPLETO vive always-on en las reglas globales (~/.gemini/GEMINI.md), así que aquí solo se recuerda
// dónde está y se anteponen los metadatos + el contexto reciente del proyecto.
const agStartHeader = "## Memoria persistente kandel — contexto del proyecto\n" +
	"El protocolo completo está en tus reglas globales (memoria kandel). Abajo van los metadatos de esta " +
	"conversación (úsalos en mem_save) y el contexto reciente del proyecto. Guarda con mem_save de forma " +
	"proactiva, en el momento en que ocurre lo que vale la pena recordar."

// agPreInvocation maneja el hook PreInvocation de Antigravity. En la primera invocación de la sesión
// (invocationNum==0) hace de arranque: corre los efectos de arranque (prepareStartup) e inyecta el
// encabezado + los metadatos internos + el contexto reciente como un ephemeralMessage. En las
// siguientes invocaciones solo dispara el backfill (barato, deja los vectores al día) sin inyectar
// nada. NOTA: guardar el prompt del usuario queda fuera de esta fase, porque requiere leer el
// transcript de Antigravity (transcriptPath), cuyo esquema aún no se ha fijado.
func (r *runner) agPreInvocation(in agInput) error {
	project := detectProject(in.cwd())
	sessionID := in.ConversationID
	r.klog(fmt.Sprintf("pre-invocation: invocationNum=%d session_id=%s project=%s", in.InvocationNum, orQ(sessionID), orQ(project)))

	if in.InvocationNum != 0 {
		// Turno posterior: leer transcript para guardar el prompt y mantener vectores.
		lastPrompt := parseTranscript(in.TranscriptPath)
		if lastPrompt != "" {
			code, _ := r.client.savePrompt(r.ctx, sessionID, lastPrompt)
			r.klog(fmt.Sprintf("POST /prompts session_id=%s -> HTTP %s", sessionID, code))
		}
		bfCode, _ := r.client.triggerBackfill(r.ctx)
		r.klog("POST /embeddings/backfill -> HTTP " + bfCode)
		return writeInjectSteps(r.out, "")
	}

	old := oldProjectName(in.cwd())
	ctxText, _ := r.prepareStartup(project, old, payload{SessionID: sessionID, CWD: in.cwd()})

	var b strings.Builder
	b.WriteString(agStartHeader)
	if meta := (agentMetadata{project: project, sessionID: sessionID}).render(); meta != "" {
		b.WriteString("\n\n")
		b.WriteString(meta)
	}
	if ctxText != "" {
		b.WriteString("\n")
		b.WriteString(ctxText)
	}
	return writeInjectSteps(r.out, b.String())
}

// agStop maneja el hook Stop de Antigravity: marca la sesión como cerrada vía la API. Devuelve un
// decision distinto de "continue" para permitir el cierre (si fuera "continue", Antigravity re-entraría
// en el bucle de ejecución, que no es lo que queremos).
func (r *runner) agStop(in agInput) error {
	if in.ConversationID != "" {
		code, _ := r.client.endSession(r.ctx, in.ConversationID)
		r.klog(fmt.Sprintf("POST /sessions/%s/end -> HTTP %s", in.ConversationID, code))
	}
	raw, _ := json.Marshal(map[string]any{"decision": "stop"})
	fmt.Fprintln(r.out, string(raw))
	return nil
}

// writeInjectSteps escribe la salida de un hook PreInvocation en el formato de Antigravity: si hay
// texto, un único paso con ephemeralMessage (mensaje de sistema transitorio que el modelo ve antes de
// responder); si no, un objeto vacío. Siempre imprime JSON válido (un hook no debe romper el flujo).
func writeInjectSteps(out io.Writer, text string) error {
	text = strings.TrimSpace(text)
	var payload map[string]any
	if text == "" {
		payload = map[string]any{}
	} else {
		payload = map[string]any{
			"injectSteps": []any{
				map[string]any{"ephemeralMessage": text},
			},
		}
	}
	raw, _ := json.Marshal(payload)
	fmt.Fprintln(out, string(raw))
	return nil
}

// parseTranscript lee el transcript JSONL de Antigravity y extrae el último prompt del usuario.
func parseTranscript(path string) (lastPrompt string) {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	for {
		var line struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if err := dec.Decode(&line); err != nil {
			break
		}
		if line.Type == "USER_INPUT" && line.Content != "" {
			lastPrompt = line.Content
		}
	}
	return lastPrompt
}
