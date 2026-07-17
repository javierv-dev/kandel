// Package embedding contiene implementaciones de ports.Embedder. La de Ollama habla por HTTP
// con un servidor Ollama (local o remoto), de modo que el binario kandel sigue siendo puro-Go
// (CGO=0): el modelo de embeddings vive en Ollama, no enlazado en el binario.
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/javierv-dev/kandel/internal/ports"
)

// Valores por defecto. El modelo bge-m3 es multilingüe (es+en) y produce vectores de 1024
// dimensiones. El modelo es contrato de datos: cambiarlo obliga a re-embedding total.
const (
	DefaultHost       = "http://127.0.0.1:11434"
	DefaultModel      = "bge-m3"
	DefaultDimensions = 1024
)

// Ollama implementa ports.Embedder llamando al endpoint /api/embed de un servidor Ollama. Ese
// endpoint acepta un texto o un arreglo de textos en `input` y responde `embeddings` (arreglo de
// vectores), lo que permite embeber contenido y título en una sola petición. Requiere una versión
// de Ollama con /api/embed (el antiguo /api/embeddings, de un solo texto, ya no se usa).
type Ollama struct {
	host       string
	model      string
	dimensions int
	client     *http.Client
	// keepAlive es el valor del campo keep_alive de /api/embed (cuánto retiene Ollama el modelo en
	// memoria). nil = no enviar el campo (default de Ollama). Es int (segundos, o -1 = siempre, 0 =
	// descargar ya) o string de duración ("1h", "30m"), según lo que traiga la config.
	keepAlive any
}

var _ ports.Embedder = (*Ollama)(nil)

// Option configura un Ollama opcionalmente.
type Option func(*Ollama)

// WithHTTPClient permite inyectar un cliente HTTP propio (útil en tests).
func WithHTTPClient(c *http.Client) Option {
	return func(o *Ollama) { o.client = c }
}

// WithKeepAlive fija el valor keep_alive que se manda en cada petición de embeddings. raw se
// interpreta así: vacío = no enviar el campo (deja el default de Ollama); un entero ("-1", "0",
// "300") = número (segundos, con -1 = siempre y 0 = descargar al instante); cualquier otra cosa
// ("1h", "30m") = string de duración que Ollama parsea. Se envía por petición, así que solo afecta
// al modelo de kandel y no al resto del servidor Ollama del usuario.
func WithKeepAlive(raw string) Option {
	return func(o *Ollama) { o.keepAlive = parseKeepAlive(raw) }
}

// parseKeepAlive convierte el valor de config en lo que espera el campo JSON keep_alive: nil para
// omitirlo, un int si es un entero (así "-1"/"0" viajan como número, no como texto), o el string tal
// cual para las duraciones ("1h", "30m").
func parseKeepAlive(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return n
	}
	return raw
}

// NewOllama crea el embedder. host vacío usa DefaultHost; model vacío usa DefaultModel.
// dimensions <= 0 usa DefaultDimensions.
func NewOllama(host, model string, dimensions int, opts ...Option) *Ollama {
	o := &Ollama{
		host:       strings.TrimRight(strings.TrimSpace(host), "/"),
		model:      strings.TrimSpace(model),
		dimensions: dimensions,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
	if o.host == "" {
		o.host = DefaultHost
	}
	if o.model == "" {
		o.model = DefaultModel
	}
	if o.dimensions <= 0 {
		o.dimensions = DefaultDimensions
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// embedRequest es el cuerpo de POST /api/embed. `input` acepta uno o varios textos; aquí siempre
// se envía como arreglo para tratar de forma uniforme el caso de un texto y el de varios.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
	// KeepAlive es opcional (omitempty): cuánto retiene Ollama el modelo cargado. nil = se omite y
	// Ollama aplica su default.
	KeepAlive any `json:"keep_alive,omitempty"`
}

// embedResponse es la respuesta de /api/embed: un vector por cada texto de entrada, en el mismo
// orden.
type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed convierte un texto en un vector. Es un caso particular de EmbedBatch con un solo texto.
func (o *Ollama) Embed(ctx context.Context, text string) ([]float32, error) {
	vectors, err := o.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

// EmbedBatch convierte varios textos en sus vectores en una sola llamada a Ollama, preservando el
// orden de entrada. Devuelve error si el servidor no responde, si el número de vectores no coincide
// con el de textos, o si algún vector vuelve vacío (no hay memoria buscable sin vector). Con lista
// vacía devuelve una lista vacía sin llamar al backend.
func (o *Ollama) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	body, err := json.Marshal(embedRequest{Model: o.model, Input: texts, KeepAlive: o.keepAlive})
	if err != nil {
		return nil, fmt.Errorf("ollama: serializar petición: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.host+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: construir petición: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: llamar a %s: %w", o.host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Ollama responde 500 cuando el modelo produce NaN/Inf en el vector y su serializador JSON no
		// puede codificarlo (bug conocido de bge-m3 con ciertos textos, ollama/ollama#15582). Es un
		// fallo POR-ENTRADA, no del backend: se envuelve en ports.ErrUnembeddable para que el backfill
		// salte esa memoria y siga. Cualquier otro estado es un fallo genérico del servidor y aborta.
		msg, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusInternalServerError && isUnembeddable(string(msg)) {
			return nil, fmt.Errorf("ollama: el modelo %q no pudo vectorizar el texto (NaN): %w", o.model, ports.ErrUnembeddable)
		}
		return nil, fmt.Errorf("ollama: respuesta %s del servidor", resp.Status)
	}

	var parsed embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("ollama: parsear respuesta: %w", err)
	}
	if len(parsed.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama: el modelo %q devolvió %d vectores para %d textos", o.model, len(parsed.Embeddings), len(texts))
	}
	for i, vec := range parsed.Embeddings {
		if len(vec) == 0 {
			return nil, fmt.Errorf("ollama: el modelo %q devolvió un vector vacío (texto %d)", o.model, i)
		}
	}
	return parsed.Embeddings, nil
}

// isUnembeddable reconoce la respuesta de Ollama cuando el vector salió con NaN/Inf y no se pudo
// serializar. Cubre tanto el mensaje del bug clásico ("...json: unsupported value: NaN") como el
// error limpio que devuelven las versiones parcheadas ("embedding contains NaN or Inf values").
func isUnembeddable(body string) bool {
	return strings.Contains(body, "NaN") || strings.Contains(body, "unsupported value")
}

// Dimensions devuelve la dimensión esperada del vector (para validar consistencia en la DB).
func (o *Ollama) Dimensions() int { return o.dimensions }

// Model identifica el modelo; se persiste junto al vector (mezclar modelos invalida distancias).
func (o *Ollama) Model() string { return o.model }
