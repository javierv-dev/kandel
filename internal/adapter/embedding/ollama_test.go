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
		if r.URL.Path != "/api/embeddings" {
			t.Errorf("ruta inesperada: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_ = json.NewEncoder(w).Encode(embedResponse{Embedding: []float32{0.1, 0.2, 0.3}})
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
	if gotBody.Model != "bge-m3" || gotBody.Prompt != "hola mundo" {
		t.Errorf("cuerpo de la petición inesperado: %+v", gotBody)
	}
	if emb.Model() != "bge-m3" || emb.Dimensions() != 3 {
		t.Errorf("model/dimensions inesperados: %q / %d", emb.Model(), emb.Dimensions())
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
		_ = json.NewEncoder(w).Encode(embedResponse{Embedding: []float32{}})
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
