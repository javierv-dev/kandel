// Package llm implementa el puerto ports.AgentRunner haciendo shell-out a la CLI de un agente
// (hoy claude) para el juicio de conflictos. El agente recibe el prompt por stdin y
// devuelve un veredicto en JSON por stdout. El binario kandel sigue siendo puro-Go (CGO=0): la
// inteligencia vive en un proceso externo, no enlazada.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// Errores centinela del adaptador.
var (
	// ErrCLINotInstalled indica que el ejecutable del agente no está en el PATH.
	ErrCLINotInstalled = errors.New("llm: la CLI del agente no está instalada")
	// ErrInvalidJSON indica que la salida del agente no era el JSON esperado.
	ErrInvalidJSON = errors.New("llm: salida del agente no es JSON válido")
	// ErrUnknownRelation indica que el agente devolvió un verbo de relación fuera del vocabulario.
	ErrUnknownRelation = errors.New("llm: relación desconocida")
)

// Runner implementa ports.AgentRunner invocando una CLI externa. El comando y sus argumentos son
// configurables (hoy claude); runCLI se inyecta en los tests para no lanzar procesos.
type Runner struct {
	name   string
	args   []string
	runCLI func(ctx context.Context, name string, args []string, stdin string) ([]byte, error)
}

var (
	_ ports.AgentRunner    = (*Runner)(nil)
	_ ports.AgentGenerator = (*Runner)(nil)
)

// New construye un Runner con el comando y argumentos dados, usando el shell-out real.
func New(name string, args ...string) *Runner {
	return &Runner{name: name, args: args, runCLI: defaultRunCLI}
}

// withRunCLI reemplaza la función de shell-out (para tests del mismo paquete).
func (r *Runner) withRunCLI(fn func(ctx context.Context, name string, args []string, stdin string) ([]byte, error)) *Runner {
	r.runCLI = fn
	return r
}

// Judge envía el prompt al agente y parsea su veredicto. En error devuelve el Verdict cero.
func (r *Runner) Judge(ctx context.Context, prompt string) (domain.Verdict, error) {
	raw, err := r.runCLI(ctx, r.name, r.args, prompt)
	if err != nil {
		return domain.Verdict{}, err
	}
	return parseVerdict(raw)
}

// Generate envía el prompt al agente y devuelve su salida de texto, desenvolviendo el sobre
// {"result":...} de las CLIs que lo usan (p. ej. claude --output-format json). Si no hay sobre,
// devuelve la salida cruda recortada. No parsea el contenido: el llamador (p. ej. el seed) decide
// cómo interpretarlo.
func (r *Runner) Generate(ctx context.Context, prompt string) (string, error) {
	raw, err := r.runCLI(ctx, r.name, r.args, prompt)
	if err != nil {
		return "", err
	}
	return unwrapResult(raw), nil
}

// unwrapResult extrae el texto útil de la salida del agente: si viene envuelto en un sobre con
// "result" (estilo claude --output-format json), devuelve su contenido; si no, la salida recortada.
func unwrapResult(raw []byte) string {
	inner := strings.TrimSpace(string(raw))
	var env envelope
	if err := json.Unmarshal([]byte(inner), &env); err == nil && strings.TrimSpace(env.Result) != "" {
		return strings.TrimSpace(env.Result)
	}
	return inner
}

// envelope captura la respuesta del agente. Algunas CLIs (p. ej. claude --output-format json)
// envuelven el resultado en un sobre con un campo "result" que contiene el JSON interno; otras
// devuelven el veredicto directo. Se admite cualquiera de las dos formas.
type envelope struct {
	Result     string                     `json:"result"`
	ModelUsage map[string]json.RawMessage `json:"modelUsage"`
}

// rawVerdict es la forma JSON que el agente debe devolver (se tolera mayúscula o minúscula
// inicial en las claves).
type rawVerdict struct {
	Relation   string  `json:"relation"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
	Model      string  `json:"model"`
	// Variantes con mayúscula inicial (compatibilidad con el prompt heredado).
	RelationU   string  `json:"Relation"`
	ConfidenceU float64 `json:"Confidence"`
	ReasoningU  string  `json:"Reasoning"`
	ModelU      string  `json:"Model"`
}

// fenceRE quita las vallas de código markdown (```json … ``` o ``` … ```).
var fenceRE = regexp.MustCompile("(?s)^```[a-zA-Z]*\\n?(.+?)\\n?```$")

// parseVerdict decodifica la salida del agente en un domain.Verdict. Acepta tanto el sobre con
// "result" como el veredicto directo, y tolera vallas de código markdown.
func parseVerdict(raw []byte) (domain.Verdict, error) {
	inner := strings.TrimSpace(string(raw))

	// Si viene envuelto en un sobre con "result", se usa el contenido interno.
	var env envelope
	if err := json.Unmarshal([]byte(inner), &env); err == nil && strings.TrimSpace(env.Result) != "" {
		inner = strings.TrimSpace(env.Result)
	}

	// Quitar vallas de código si las hay.
	if m := fenceRE.FindStringSubmatch(inner); len(m) == 2 {
		inner = strings.TrimSpace(m[1])
	}

	var rv rawVerdict
	if err := json.Unmarshal([]byte(inner), &rv); err != nil {
		return domain.Verdict{}, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}

	relation := firstNonEmpty(rv.Relation, rv.RelationU)
	confidence := rv.Confidence
	if confidence == 0 {
		confidence = rv.ConfidenceU
	}
	reasoning := firstNonEmpty(rv.Reasoning, rv.ReasoningU)
	model := firstNonEmpty(rv.Model, rv.ModelU)
	if model == "" {
		for k := range env.ModelUsage {
			model = k
			break
		}
	}

	rel := domain.RelationType(relation)
	if !rel.Valid() || rel == domain.RelationPending {
		return domain.Verdict{}, fmt.Errorf("%w: %q", ErrUnknownRelation, relation)
	}
	return domain.Verdict{
		Relation:   rel,
		Confidence: confidence,
		Reasoning:  reasoning,
		Model:      model,
	}, nil
}

// firstNonEmpty devuelve el primer argumento no vacío.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// defaultRunCLI ejecuta el comando externo, pasando el prompt por stdin, y devuelve su stdout.
// Traduce el "ejecutable no encontrado" a ErrCLINotInstalled.
func defaultRunCLI(ctx context.Context, name string, args []string, stdin string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	hideWindow(cmd)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// El detalle del fallo puede venir por stderr o, con --output-format json, por stdout (el
			// sobre del error). Se incluyen ambos para que el motivo real sea visible en el log.
			detail := strings.TrimSpace(string(exitErr.Stderr))
			if so := strings.TrimSpace(string(out)); so != "" {
				detail = strings.TrimSpace(detail + " " + truncate(so, 600))
			}
			return nil, fmt.Errorf("llm: la CLI %q terminó con código %d: %s", name, exitErr.ExitCode(), detail)
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%w: %q", ErrCLINotInstalled, name)
		}
		return nil, err
	}
	return out, nil
}

// truncate recorta s a n caracteres añadiendo una marca, para no inflar el mensaje de error.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…[recortado]"
}
