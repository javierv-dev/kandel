package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/javierv-dev/kandel/internal/domain"
)

// writeJSON serializa v como respuesta JSON con el código dado.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError responde un error en JSON con el código dado.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeBody deserializa el cuerpo JSON de la petición en dst. Devuelve false (y ya responde 400)
// si el cuerpo no es JSON válido.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "cuerpo JSON inválido: "+err.Error())
		return false
	}
	return true
}

// pathID lee el segmento {id} de la ruta como int64. Devuelve false (y ya responde 400) si no es
// un número válido.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido: "+raw)
		return 0, false
	}
	return id, true
}

// queryInt lee un parámetro de consulta entero; def si falta o no es número.
func queryInt(r *http.Request, name string, def int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

// queryFloat lee un parámetro de consulta de punto flotante; def si falta o no es número.
func queryFloat(r *http.Request, name string, def float64) float64 {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return def
	}
	return f
}

// parseIDSet lee una lista de ids separados por coma ("12,7,40") en un conjunto. Ignora entradas
// vacías o no numéricas. Devuelve nil si no hay ninguna (leer de un mapa nil es seguro).
func parseIDSet(raw string) map[int64]bool {
	if raw == "" {
		return nil
	}
	var set map[int64]bool
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			continue
		}
		if set == nil {
			set = make(map[int64]bool)
		}
		set[id] = true
	}
	return set
}

// statusForError traduce errores de dominio conocidos a códigos HTTP. Los no reconocidos van a
// 500 (error interno) o 400 según si son de validación.
func statusForError(err error) int {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrAmbiguousProject):
		return http.StatusBadRequest
	case isValidationError(err):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// isValidationError indica si el error es una de las invariantes de dominio (entrada inválida),
// que deben devolver 400 en vez de 500.
func isValidationError(err error) bool {
	for _, ve := range []error{
		domain.ErrEmptyTitle, domain.ErrEmptyContent, domain.ErrInvalidType,
		domain.ErrInvalidScope,
	} {
		if errors.Is(err, ve) {
			return true
		}
	}
	return false
}
