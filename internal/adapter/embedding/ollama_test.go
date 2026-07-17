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

func TestOllamaDefaults(t *testing.T) {
	emb := NewOllama("", "", 0)
	if emb.Model() != DefaultModel || emb.Dimensions() != DefaultDimensions {
		t.Errorf("defaults incorrectos: %q / %d", emb.Model(), emb.Dimensions())
	}
	if !strings.HasPrefix(emb.host, "http") {
		t.Errorf("host por defecto inesperado: %q", emb.host)
	}
}
