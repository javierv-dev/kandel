// Package hub es el servidor de sincronización (change-feed) del lado servidor: un keyed store
// genérico y sin lógica de dominio. NO corre lógica kandel (ni embeddings, ni juez de conflictos, ni
// reglas de dominio): solo guarda una fila por (namespace, token, key), la ordena con una secuencia
// monótona (server_seq) y arbitra por LWW sobre updated_at al recibir. El mismo hub sirve a cualquier
// namespace (memory, skills, …); por eso el payload es opaco (no lo interpreta).
//
// El hub corre en el mismo binario kandel (modo `kandel serve --sync-hub`), pero es un proceso
// aparte del daemon local: el daemon es el lado CLIENTE del sync (ver internal/adapter/httpsync).
package hub

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Change es una fila del change-feed tal como la ve el hub. El payload es opaco: el hub no lo
// interpreta. Op se guarda sin validar (el hub no conoce el dominio); upsert y delete (tombstone) se
// tratan igual: ambos son filas que se guardan y se sirven.
type Change struct {
	Key       string
	Op        string
	Payload   []byte
	UpdatedAt time.Time // árbitro de LWW
	Project   string
}

// Store es el almacén keyed del hub. Guarda una fila por (ns, token, key) con una secuencia monótona
// server_seq por (ns, token). Las implementaciones deben serializar las escrituras por (ns, token)
// para que el bump de server_seq sea consistente.
type Store interface {
	// Apply aplica un lote de filas con LWW por UpdatedAt: cada fila reemplaza a la guardada solo si
	// es estrictamente más nueva, y en ese caso bumpea server_seq. Filas más viejas o iguales se
	// ignoran.
	Apply(ctx context.Context, ns, token string, changes []Change) error
	// Since devuelve las filas con server_seq > cursor (en orden) y el nuevo cursor (el mayor
	// server_seq servido, o el mismo cursor si no hubo filas nuevas).
	Since(ctx context.Context, ns, token string, cursor int64) (changes []Change, newCursor int64, err error)
}

// handlerOptions recoge los ajustes opcionales del Handler. El paquete hub NO lee config a propósito
// (es un store genérico sin lógica de dominio); los ajustes los inyecta el servidor (serveHub).
type handlerOptions struct {
	ratePerMinute int
	rateBurst     int
}

// Option configura el Handler.
type Option func(*handlerOptions)

// WithRateLimit activa el rate limit por IP: hasta perMinute peticiones por minuto con ráfagas de hasta
// burst. perMinute <= 0 lo desactiva (sin límite). burst <= 0 usa perMinute como ráfaga.
func WithRateLimit(perMinute, burst int) Option {
	return func(o *handlerOptions) {
		o.ratePerMinute = perMinute
		o.rateBurst = burst
	}
}

// Handler construye el enrutador del hub. Rutas namespaced con token en la ruta y como Bearer. Con
// WithRateLimit, envuelve el enrutador en un limitador por IP (defensa en profundidad contra fuerza
// bruta sobre el token).
func Handler(store Store, opts ...Option) http.Handler {
	var o handlerOptions
	for _, opt := range opts {
		opt(&o)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /{ns}/{token}/changes", handleApply(store))
	mux.HandleFunc("GET /{ns}/{token}/changes", handleSince(store))

	var h http.Handler = mux
	if o.ratePerMinute > 0 {
		burst := o.rateBurst
		if burst <= 0 {
			burst = o.ratePerMinute
		}
		h = newRateLimiter(o.ratePerMinute, burst, nil).middleware(h)
	}
	return h
}

// wireChange es la forma de red de una fila. DEBE coincidir con la del cliente
// (internal/adapter/httpsync): el payload viaja como JSON embebido, no como base64.
type wireChange struct {
	Key       string          `json:"key"`
	Op        string          `json:"op"`
	Payload   json.RawMessage `json:"payload"`
	UpdatedAt time.Time       `json:"updated_at"`
	Project   string          `json:"project,omitempty"`
}

type applyRequest struct {
	Changes []wireChange `json:"changes"`
}

type sinceResponse struct {
	Changes []wireChange `json:"changes"`
	Cursor  int64        `json:"cursor"`
}

// handleApply recibe un lote de filas (POST .../changes) y las aplica con LWW.
func handleApply(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ns, token, ok := route(w, r)
		if !ok {
			return
		}
		var req applyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "cuerpo JSON inválido: "+err.Error())
			return
		}
		changes := make([]Change, len(req.Changes))
		for i, wc := range req.Changes {
			changes[i] = Change{
				Key:       wc.Key,
				Op:        wc.Op,
				Payload:   []byte(wc.Payload),
				UpdatedAt: wc.UpdatedAt,
				Project:   wc.Project,
			}
		}
		if err := store.Apply(r.Context(), ns, token, changes); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// handleSince sirve las filas con server_seq > cursor (GET .../changes?since=).
func handleSince(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ns, token, ok := route(w, r)
		if !ok {
			return
		}
		cursor, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		changes, newCursor, err := store.Since(r.Context(), ns, token, cursor)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resp := sinceResponse{Changes: make([]wireChange, len(changes)), Cursor: newCursor}
		for i, ch := range changes {
			resp.Changes[i] = wireChange{
				Key:       ch.Key,
				Op:        ch.Op,
				Payload:   json.RawMessage(ch.Payload),
				UpdatedAt: ch.UpdatedAt,
				Project:   ch.Project,
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// route extrae ns y token de la ruta y valida el Bearer. El token de la ruta es el único control de
// acceso: debe coincidir con el Bearer (comparación de tiempo constante). ns/token vacíos o auth
// inválida → responde y devuelve ok=false.
func route(w http.ResponseWriter, r *http.Request) (ns, token string, ok bool) {
	ns = r.PathValue("ns")
	token = r.PathValue("token")
	if ns == "" || token == "" {
		writeError(w, http.StatusBadRequest, "namespace o token vacío")
		return "", "", false
	}
	if !bearerMatches(r, token) {
		writeError(w, http.StatusUnauthorized, "token inválido")
		return "", "", false
	}
	return ns, token, true
}

// bearerMatches indica si el header Authorization: Bearer coincide con el token esperado, en tiempo
// constante para no filtrar el token por temporización.
func bearerMatches(r *http.Request, token string) bool {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	got := strings.TrimPrefix(h, prefix)
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
