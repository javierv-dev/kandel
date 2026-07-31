package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/javierv-dev/kandel/internal/ports"
)

func TestOllamaEmbed(t *testing.T) {
	var gotBody embedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("ruta inesperada: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_ = json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{0.1, 0.2, 0.3}}})
	}))
	defer server.Close()

	emb := NewOllama(server.URL, "bge-m3", 3)
	vec, err := emb.Embed(context.Background(), "hola mundo")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 || vec[0] != 0.1 {
		t.Errorf("vector inesperado: %v", vec)
	}
	if gotBody.Model != "bge-m3" || len(gotBody.Input) != 1 || gotBody.Input[0] != "hola mundo" {
		t.Errorf("cuerpo de la petición inesperado: %+v", gotBody)
	}
	if emb.Model() != "bge-m3" || emb.Dimensions() != 3 {
		t.Errorf("model/dimensions inesperados: %q / %d", emb.Model(), emb.Dimensions())
	}
}

func TestOllamaEmbedBatch(t *testing.T) {
	var gotBody embedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("ruta inesperada: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		// Un vector por texto de entrada, en el mismo orden.
		out := make([][]float32, len(gotBody.Input))
		for i := range gotBody.Input {
			out[i] = []float32{float32(i), 0.2, 0.3}
		}
		_ = json.NewEncoder(w).Encode(embedResponse{Embeddings: out})
	}))
	defer server.Close()

	emb := NewOllama(server.URL, "bge-m3", 3)
	vectors, err := emb.EmbedBatch(context.Background(), []string{"contenido", "titulo"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vectors) != 2 || vectors[0][0] != 0 || vectors[1][0] != 1 {
		t.Errorf("vectores inesperados: %v", vectors)
	}
	if len(gotBody.Input) != 2 || gotBody.Input[1] != "titulo" {
		t.Errorf("cuerpo de la petición inesperado: %+v", gotBody)
	}
}

func TestOllamaEmbedBatchEmptyInputSkipsCall(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer server.Close()

	emb := NewOllama(server.URL, "bge-m3", 3)
	vectors, err := emb.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("EmbedBatch vacío: %v", err)
	}
	if len(vectors) != 0 {
		t.Errorf("se esperaba lista vacía, se obtuvo: %v", vectors)
	}
	if called {
		t.Error("una entrada vacía no debe llamar al backend")
	}
}

func TestOllamaEmbedBatchCountMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Devuelve menos vectores que textos pedidos.
		_ = json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{0.1, 0.2, 0.3}}})
	}))
	defer server.Close()

	emb := NewOllama(server.URL, "bge-m3", 3)
	if _, err := emb.EmbedBatch(context.Background(), []string{"a", "b"}); err == nil {
		t.Error("un número de vectores distinto al de textos debe ser un error")
	}
}

func TestOllamaEmbedServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	emb := NewOllama(server.URL, "bge-m3", 3)
	if _, err := emb.Embed(context.Background(), "x"); err == nil {
		t.Error("un error del servidor debe propagarse")
	}
}

func TestOllamaEmbedNaNIsUnembeddable(t *testing.T) {
	// Ollama devuelve 500 con este cuerpo cuando bge-m3 produce NaN en el vector; debe mapearse a
	// ports.ErrUnembeddable (fallo por-entrada) y no a un error genérico de servidor.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to encode response: json: unsupported value: NaN"}`))
	}))
	defer server.Close()

	emb := NewOllama(server.URL, "bge-m3", 3)
	if _, err := emb.Embed(context.Background(), "texto envenenado"); !errors.Is(err, ports.ErrUnembeddable) {
		t.Errorf("un 500 por NaN debe ser ports.ErrUnembeddable, se obtuvo: %v", err)
	}
}

func TestOllamaEmbedEmptyVector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{}}})
	}))
	defer server.Close()

	emb := NewOllama(server.URL, "bge-m3", 3)
	if _, err := emb.Embed(context.Background(), "x"); err == nil {
		t.Error("un vector vacío debe ser un error")
	}
}

// TestOllamaKeepAliveNumber verifica que un keep_alive entero ("-1") viaja como número JSON en el
// cuerpo, para que Ollama lo interprete como "mantener cargado siempre" (no como texto).
func TestOllamaKeepAliveNumber(t *testing.T) {
	var raw map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &raw)
		_ = json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{0.1, 0.2, 0.3}}})
	}))
	defer server.Close()

	emb := NewOllama(server.URL, "bge-m3", 3, WithKeepAlive("-1"))
	if _, err := emb.Embed(context.Background(), "x"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	// JSON decodifica los números a float64.
	if v, ok := raw["keep_alive"].(float64); !ok || v != -1 {
		t.Errorf("keep_alive esperado -1 (número), se obtuvo: %#v", raw["keep_alive"])
	}
}

// TestOllamaKeepAliveDuration verifica que una duración ("1h") viaja como string en el cuerpo.
func TestOllamaKeepAliveDuration(t *testing.T) {
	var raw map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &raw)
		_ = json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{0.1, 0.2, 0.3}}})
	}))
	defer server.Close()

	emb := NewOllama(server.URL, "bge-m3", 3, WithKeepAlive("1h"))
	if _, err := emb.Embed(context.Background(), "x"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if v, ok := raw["keep_alive"].(string); !ok || v != "1h" {
		t.Errorf("keep_alive esperado \"1h\" (string), se obtuvo: %#v", raw["keep_alive"])
	}
}

// TestOllamaKeepAliveOmitted verifica que sin keep_alive (o vacío) el campo NO se envía, dejando el
// default de Ollama.
func TestOllamaKeepAliveOmitted(t *testing.T) {
	var raw map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &raw)
		_ = json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{0.1, 0.2, 0.3}}})
	}))
	defer server.Close()

	emb := NewOllama(server.URL, "bge-m3", 3, WithKeepAlive("  "))
	if _, err := emb.Embed(context.Background(), "x"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if _, present := raw["keep_alive"]; present {
		t.Errorf("keep_alive no debía enviarse, pero está presente: %#v", raw["keep_alive"])
	}
}

// TestOllamaNumCtx verifica que un num_ctx > 0 viaja como options.num_ctx en el cuerpo, para que
// Ollama cargue el modelo con contexto acotado (huella de memoria) y no con su máximo.
func TestOllamaNumCtx(t *testing.T) {
	var raw map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &raw)
		_ = json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{0.1, 0.2, 0.3}}})
	}))
	defer server.Close()

	emb := NewOllama(server.URL, "qwen3-embedding:0.6b", 3, WithNumCtx("4096"))
	if _, err := emb.Embed(context.Background(), "x"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	opts, ok := raw["options"].(map[string]any)
	if !ok {
		t.Fatalf("options no presente o con forma inesperada: %#v", raw["options"])
	}
	if v, ok := opts["num_ctx"].(float64); !ok || v != 4096 {
		t.Errorf("num_ctx esperado 4096, se obtuvo: %#v", opts["num_ctx"])
	}
}

// TestOllamaNumCtxOmitted verifica que con num_ctx vacío, cero o inválido el campo options NO se
// envía, dejando que Ollama use el contexto máximo del modelo.
func TestOllamaNumCtxOmitted(t *testing.T) {
	for _, rawCfg := range []string{"", "0", "-8", "abc"} {
		var raw map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &raw)
			_ = json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{0.1, 0.2, 0.3}}})
		}))

		emb := NewOllama(server.URL, "qwen3-embedding:0.6b", 3, WithNumCtx(rawCfg))
		if _, err := emb.Embed(context.Background(), "x"); err != nil {
			t.Fatalf("Embed con num_ctx %q: %v", rawCfg, err)
		}
		if _, present := raw["options"]; present {
			t.Errorf("options no debía enviarse con num_ctx %q, pero está presente: %#v", rawCfg, raw["options"])
		}
		server.Close()
	}
}

// TestParseKeepAlive cubre el mapeo de valores de config al tipo del campo JSON.
func TestParseKeepAlive(t *testing.T) {
	if got := parseKeepAlive(""); got != nil {
		t.Errorf("vacío debe dar nil, dio %#v", got)
	}
	if got := parseKeepAlive("  "); got != nil {
		t.Errorf("solo espacios debe dar nil, dio %#v", got)
	}
	if got := parseKeepAlive("-1"); got != -1 {
		t.Errorf("\"-1\" debe dar int -1, dio %#v", got)
	}
	if got := parseKeepAlive("0"); got != 0 {
		t.Errorf("\"0\" debe dar int 0, dio %#v", got)
	}
	if got := parseKeepAlive("30m"); got != "30m" {
		t.Errorf("\"30m\" debe dar string \"30m\", dio %#v", got)
	}
}

func TestOllamaDefaults(t *testing.T) {
	emb := NewOllama("", "", 0)
	if emb.Model() != DefaultModel || emb.Dimensions() != DefaultDimensions {
		t.Errorf("defaults incorrectos: %q / %d", emb.Model(), emb.Dimensions())
	}
	if !strings.HasPrefix(emb.host, "http") {
		t.Errorf("host por defecto inesperado: %q", emb.host)
	}
}

// captureInput levanta un servidor que anota el `input` de /api/embed y responde un vector fijo.
// Sirve para comprobar QUÉ texto se manda, que es lo único que distingue Embed de EmbedQuery.
func captureInput(t *testing.T, got *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body embedRequest
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		*got = body.Input
		_ = json.NewEncoder(w).Encode(embedResponse{Embeddings: [][]float32{{1, 0, 0}}})
	}))
}

// Con qwen3 la consulta viaja envuelta en la instrucción: sin ella el ruido solapa con los
// aciertos y el umbral de la inyección por relevancia deja de separar nada.
func TestOllamaEmbedQueryAddsInstructForQwen3(t *testing.T) {
	var got []string
	server := captureInput(t, &got)
	defer server.Close()

	emb := NewOllama(server.URL, "qwen3-embedding:0.6b", 3)
	if _, err := emb.EmbedQuery(context.Background(), "cómo se resuelve el proyecto"); err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("se esperaba 1 input, hubo %d", len(got))
	}
	if !strings.HasPrefix(got[0], queryInstruct) {
		t.Errorf("la consulta debe llevar la instrucción delante; se mandó: %q", got[0])
	}
	if !strings.HasSuffix(got[0], "cómo se resuelve el proyecto") {
		t.Errorf("la consulta original debe conservarse al final; se mandó: %q", got[0])
	}
}

// El lado DOCUMENTO nunca se envuelve, ni siquiera con qwen3: el índice guarda texto crudo. Si
// esto se rompiera, los vectores guardados dejarían de ser comparables con los ya existentes.
func TestOllamaEmbedDoesNotAddInstruct(t *testing.T) {
	var got []string
	server := captureInput(t, &got)
	defer server.Close()

	emb := NewOllama(server.URL, "qwen3-embedding:0.6b", 3)
	if _, err := emb.Embed(context.Background(), "contenido de la memoria"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 1 || got[0] != "contenido de la memoria" {
		t.Errorf("el documento debe viajar crudo; se mandó: %q", got)
	}
}

// Los modelos simétricos no reciben instrucción: para ellos es texto parásito dentro de la
// consulta. Cubre también el modelo desconocido, que se trata como simétrico por seguridad.
func TestOllamaEmbedQueryLeavesSymmetricModelsAlone(t *testing.T) {
	for _, model := range []string{"bge-m3", "bge-m3:latest", "snowflake-arctic-embed2:latest", "modelo-inventado"} {
		var got []string
		server := captureInput(t, &got)
		emb := NewOllama(server.URL, model, 3)
		if _, err := emb.EmbedQuery(context.Background(), "una consulta"); err != nil {
			server.Close()
			t.Fatalf("EmbedQuery con %s: %v", model, err)
		}
		server.Close()
		if len(got) != 1 || got[0] != "una consulta" {
			t.Errorf("%s no debe recibir instrucción; se mandó: %q", model, got)
		}
	}
}

// La detección es por prefijo del nombre, para cubrir las variantes de tamaño y etiqueta.
func TestWantsQueryInstruct(t *testing.T) {
	quiere := []string{"qwen3-embedding:0.6b", "qwen3-embedding:4b", "qwen3-embedding:latest",
		"QWEN3-EMBEDDING:0.6B", "  qwen3-embedding:8b  "}
	for _, m := range quiere {
		if !wantsQueryInstruct(m) {
			t.Errorf("%q debería querer instrucción", m)
		}
	}
	noQuiere := []string{"bge-m3", "bge-m3:latest", "snowflake-arctic-embed2:latest",
		"nomic-embed-text", "", "qwen2-embedding:0.6b"}
	for _, m := range noQuiere {
		if wantsQueryInstruct(m) {
			t.Errorf("%q NO debería querer instrucción", m)
		}
	}
}
