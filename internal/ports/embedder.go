package ports

import (
	"context"
	"errors"
)

// ErrUnembeddable indica que el backend aceptó la petición pero no pudo producir un vector válido
// para ESE texto en particular (p. ej. bge-m3 en Ollama devuelve NaN para ciertas secuencias de
// tokens y Ollama, que no serializa NaN a JSON, responde 500). Es un fallo POR-ENTRADA, no del
// backend: el embebedor sigue sano para otros textos. El backfill lo distingue de una caída (red o
// 500 genérico) para SALTAR la memoria envenenada y seguir, en vez de abortar toda la pasada.
var ErrUnembeddable = errors.New("embebedor: texto no vectorizable")

// Embedder convierte texto en un vector. Es la pieza que hace posible la búsqueda semántica
// del agente. El default es Ollama por HTTP (binario kandel sigue puro-Go, CGO=0). El mismo
// modelo debe usarse al guardar y al buscar: mezclar modelos invalida las distancias de coseno.
//
// Documento y consulta NO son intercambiables. Algunos modelos son asimétricos: esperan el
// documento crudo pero la consulta envuelta en una instrucción que declara la tarea de
// recuperación. Por eso hay dos entradas —Embed para documentos, EmbedQuery para consultas— y
// el formato concreto lo decide el adaptador, que es quien conoce su modelo.
type Embedder interface {
	// Embed convierte texto en un vector. Es el lado DOCUMENTO: lo que se guarda en el índice
	// (contenido y título de una memoria). El texto viaja tal cual, sin envolver.
	Embed(ctx context.Context, text string) ([]float32, error)
	// EmbedQuery convierte una consulta de búsqueda en un vector. Es el lado CONSULTA: el texto
	// con el que se interroga al índice, no lo que se guarda en él. En modelos simétricos
	// equivale a Embed; en los asimétricos el adaptador antepone la instrucción que ese modelo
	// espera. Usarlo importa: con qwen3-embedding, embeber la consulta cruda sube el suelo de
	// ruido hasta solapar con los aciertos y ningún umbral logra separarlos.
	EmbedQuery(ctx context.Context, query string) ([]float32, error)
	// EmbedBatch convierte varios textos en sus vectores en una sola petición, preservando el
	// orden de entrada (el vector i corresponde al texto i). Permite embeber contenido y título de
	// una memoria en un único viaje de red. Con lista vacía devuelve una lista vacía sin llamar al
	// backend.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	// Dimensions indica el tamaño del vector (para validar consistencia en la base de datos).
	Dimensions() int
	// Model identifica el modelo (se persiste junto al vector: es parte del contrato de datos).
	Model() string
}
