package embedding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
