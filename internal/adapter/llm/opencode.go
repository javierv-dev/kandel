package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// openCodeRunner implementa el contrato de CLI de OpenCode para el juez de conflictos y el seed.
//
// `opencode run` no devuelve el texto pelado: con `--format json` emite un flujo JSONL de eventos
// (step_start, text, step_finish, …). Este runner pasa el prompt por stdin y reconstruye el texto
// del asistente concatenando las partes de tipo "text"; después lo entrega a parseVerdict/Generate.
// `--archived` mantiene las sesiones efímeras fuera de las listas de OpenCode (uso scripted/CI).
type openCodeRunner struct {
	model  string
	runCLI func(ctx context.Context, name string, args []string, stdin string) ([]byte, error)
}

var (
	_ ports.AgentRunner    = (*openCodeRunner)(nil)
	_ ports.AgentGenerator = (*openCodeRunner)(nil)
)

// newOpenCode construye el runner de OpenCode. Un modelo vacío usa el default del registro.
func newOpenCode(model string) *openCodeRunner {
	model = strings.TrimSpace(model)
	if model == "" {
		model = modelsByAgent["opencode"].Judge
	}
	return &openCodeRunner{model: model, runCLI: defaultRunCLI}
}

// args devuelve los argumentos de `opencode run`. El prompt va por stdin (ver defaultRunCLI).
func (o *openCodeRunner) args() []string {
	return []string{"run", "--archived", "--format", "json", "--model", o.model}
}

// Judge envía el prompt, extrae el texto del asistente y lo parsea como veredicto. Si el modelo no
// incluyó su nombre en el JSON, se rellena con el modelo configurado para trazabilidad.
func (o *openCodeRunner) Judge(ctx context.Context, prompt string) (domain.Verdict, error) {
	text, err := o.Generate(ctx, prompt)
	if err != nil {
		return domain.Verdict{}, err
	}
	v, err := parseVerdict([]byte(text))
	if err != nil {
		return domain.Verdict{}, err
	}
	if strings.TrimSpace(v.Model) == "" {
		v.Model = o.model
	}
	return v, nil
}

// Generate envía el prompt y devuelve el texto del asistente ya desenvuelto del flujo de eventos.
func (o *openCodeRunner) Generate(ctx context.Context, prompt string) (string, error) {
	raw, err := o.runCLI(ctx, "opencode", o.args(), prompt)
	if err != nil {
		return "", err
	}
	return parseOpenCodeStream(raw)
}

// openCodeEvent es el sobre mínimo de una línea del flujo `--format json`. Los eventos que no son
// texto (step_start, step_finish, tool, …) se ignoran.
type openCodeEvent struct {
	Type string `json:"type"`
	Part struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"part"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// parseOpenCodeStream reconstruye el texto del asistente a partir del JSONL de OpenCode. Tolera
// salida en texto plano (versiones o flags sin --format json) y convierte un evento de error del
// proveedor en un error de Go para que el juez lo registre en alerts.json.
func parseOpenCodeStream(raw []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var text strings.Builder
	sawEvent := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev openCodeEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			// Línea no-JSON: salida en texto plano.
			text.WriteString(line)
			text.WriteString("\n")
			continue
		}
		sawEvent = true
		if ev.Error != nil && strings.TrimSpace(ev.Error.Message) != "" {
			return "", fmt.Errorf("llm: opencode: %s", ev.Error.Message)
		}
		if ev.Type == "text" && ev.Part.Type == "text" {
			text.WriteString(ev.Part.Text)
			continue
		}
		// JSON suelto (p. ej. un veredicto impreso sin sobre de eventos).
		if ev.Type == "" && ev.Part.Type == "" && ev.Part.Text == "" {
			text.WriteString(line)
			text.WriteString("\n")
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("llm: leer salida de opencode: %w", err)
	}

	out := strings.TrimSpace(text.String())
	if out == "" {
		if sawEvent {
			return "", fmt.Errorf("%w: opencode no devolvió texto", ErrInvalidJSON)
		}
		return "", fmt.Errorf("%w: opencode devolvió salida vacía", ErrInvalidJSON)
	}
	return out, nil
}
