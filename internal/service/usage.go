package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// dormantMinInjections es cuántas veces hay que haber inyectado una memoria antes de contarla como
// "dormida" (nunca mencionada). Por debajo de esto la muestra es demasiado pequeña: una memoria que
// salió dos veces y no se usó puede ser simple mala suerte del ranking, no una memoria inútil.
const dormantMinInjections = 5

// transcriptScanLimit acota cuánto de la transcripción se lee (bytes por línea). Las transcripciones
// de sesiones largas tienen líneas grandes y esto corre al CERRAR la sesión, donde colgarse es
// especialmente molesto: es preferible medir de menos que retrasar el cierre.
const transcriptScanLimit = 4 * 1024 * 1024

// UsageStat es la telemetría de una memoria ya cruzada con su título, para poder presentarla sin
// que quien la muestra tenga que resolver los ids uno a uno.
type UsageStat struct {
	Memory domain.Memory
	Usage  domain.MemoryUsage
}

// MentionResult resume lo que encontró un barrido de transcripción.
type MentionResult struct {
	Scanned   int // memorias inyectadas que se comprobaron
	Mentioned int // de esas, cuántas aparecieron en las respuestas del agente
}

// UsageService lleva el lazo de realimentación de la memoria: cuántas veces se inyecta cada una y
// cuántas el agente acaba usándola. Sin esta medición no se puede podar —se guarda mucho, no se sabe
// qué sirve— y cualquier ajuste posterior de umbrales o campos es a ciegas.
//
// La medición de uso es deliberadamente TOSCA: busca el id o el título de la memoria en las
// respuestas del agente. No distingue "la usó para razonar" de "la nombró de paso", y no pretende
// hacerlo. "Inyectada 14 veces, nunca mencionada" ya es accionable sin más precisión, y la
// alternativa —pedirle al agente que se autoevalúe al cerrar— no es fiable y cuesta tokens en cada
// cierre de sesión.
type UsageService struct {
	usage    ports.UsageRepo
	memories ports.MemoryRepo
	clock    ports.Clock
}

// NewUsageService construye el servicio con sus dependencias inyectadas.
func NewUsageService(usage ports.UsageRepo, memories ports.MemoryRepo, clock ports.Clock) *UsageService {
	return &UsageService{usage: usage, memories: memories, clock: clock}
}

// RecordInjected anota que estas memorias se inyectaron ahora. Corre en el camino caliente (una vez
// por prompt del usuario), así que el llamador debe tratarlo como best-effort: perder una anotación
// solo desafina una estadística, nunca debe impedir que la inyección llegue al agente.
func (s *UsageService) RecordInjected(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.usage.RecordInjected(ctx, ids, s.clock.Now())
}

// RecordMentions lee la transcripción de una sesión y anota, de las memorias que se le inyectaron,
// cuáles aparecen en las RESPUESTAS del agente.
//
// Que sean solo las respuestas es la parte que hace que la medición signifique algo: el bloque
// inyectado viaja dentro del turno del usuario, así que el título de toda memoria inyectada aparece
// siempre en la transcripción. Contarlo sin filtrar por rol daría 100 % de menciones y no mediría
// nada.
func (s *UsageService) RecordMentions(ctx context.Context, ids []int64, transcriptPath string) (MentionResult, error) {
	if len(ids) == 0 || strings.TrimSpace(transcriptPath) == "" {
		return MentionResult{}, nil
	}
	replies, err := assistantText(transcriptPath)
	if err != nil {
		return MentionResult{}, err
	}
	if replies == "" {
		return MentionResult{Scanned: len(ids)}, nil
	}

	var mentioned []int64
	for _, id := range ids {
		mem, err := s.memories.Get(ctx, id)
		if err != nil || mem == nil {
			continue
		}
		if mentionsMemory(replies, id, mem.Title) {
			mentioned = append(mentioned, id)
		}
	}
	if err := s.usage.RecordMentioned(ctx, mentioned, s.clock.Now()); err != nil {
		return MentionResult{}, err
	}
	return MentionResult{Scanned: len(ids), Mentioned: len(mentioned)}, nil
}

// Stats devuelve las memorias más inyectadas con su título resuelto, de más a menos. Es la vista que
// contesta "¿qué está sirviendo la memoria de verdad?".
func (s *UsageService) Stats(ctx context.Context, limit int) ([]UsageStat, error) {
	rows, err := s.usage.TopInjected(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]UsageStat, 0, len(rows))
	for _, u := range rows {
		mem, err := s.memories.Get(ctx, u.MemoryID)
		if err != nil || mem == nil {
			continue
		}
		out = append(out, UsageStat{Memory: *mem, Usage: u})
	}
	return out, nil
}

// CountDormant cuenta las memorias que se inyectan lo bastante como para juzgarlas y nunca se
// mencionan: las candidatas a poda.
func (s *UsageService) CountDormant(ctx context.Context) (int, error) {
	return s.usage.CountDormant(ctx, dormantMinInjections)
}

// transcriptEntry es el subconjunto de una línea de la transcripción que hace falta para quedarse
// con las respuestas del agente. El resto de campos se ignora: el formato completo pertenece al
// agente y puede cambiar, así que solo se depende de lo mínimo (el rol y el texto).
type transcriptEntry struct {
	Type    string `json:"type"`
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// assistantText extrae y concatena el texto de los turnos del ASISTENTE de una transcripción JSONL.
// Tolerante por diseño: una línea que no parsea se salta en vez de abortar el barrido, porque el
// formato lo fija el agente y una versión nueva no debe romper el cierre de sesión.
func assistantText(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // sin transcripción no hay nada que medir; no es un fallo
		}
		return "", fmt.Errorf("abrir la transcripción: %w", err)
	}
	defer f.Close()

	var b strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), transcriptScanLimit)
	for sc.Scan() {
		var e transcriptEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if e.Type != "assistant" && e.Message.Role != "assistant" {
			continue
		}
		b.WriteString(contentText(e.Message.Content))
		b.WriteByte('\n')
	}
	// Un error del escáner (línea gigante, E/S) deja lo leído hasta ahí: media medición vale más que
	// ninguna, y esto nunca debe tumbar el cierre de la sesión.
	return b.String(), nil
}

// contentText aplana el campo content de un mensaje a texto plano. Acepta las dos formas que usan
// los agentes: una cadena suelta, o una lista de bloques con un campo text.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Text != "" {
			b.WriteString(blk.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// mentionsMemory indica si el texto de las respuestas hace referencia a una memoria, por su id
// ("id 356", como la superficia el contexto inyectado) o por su título literal.
//
// El título se compara en minúsculas y solo si es lo bastante largo: un título de dos palabras
// genéricas ("Config inicial") aparecería por casualidad en cualquier conversación del proyecto y
// convertiría la señal en ruido.
func mentionsMemory(replies string, id int64, title string) bool {
	if idMentionRE(id).MatchString(replies) {
		return true
	}
	t := strings.ToLower(strings.TrimSpace(title))
	if len(t) < 20 {
		return false
	}
	return strings.Contains(strings.ToLower(replies), t)
}

// idMentionRE compila el patrón de mención por id: "id 356" o "#356", con frontera de palabra
// detrás para que 356 no case dentro de 3567.
func idMentionRE(id int64) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`(?i)(\bid\s*#?%d|#%d)\b`, id, id))
}
