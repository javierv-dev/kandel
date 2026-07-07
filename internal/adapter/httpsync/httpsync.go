// Package httpsync implementa ports.SyncClient sobre HTTP: es el transporte del sync por change-feed
// contra el hub. Mantiene el binario puro-Go (CGO=0) y sin estado de dominio: solo serializa filas y
// habla con el hub por JSON. El hub es genérico (namespaced), así que el mismo cliente sirve para
// memory, skills, etc. cambiando el argumento ns.
package httpsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// Client habla con el hub por HTTP. Solo guarda el host y el cliente HTTP: el token/namespace viaja
// por llamada como el argumento target (identifica el slot del hub y autentica como Bearer), de modo
// que un mismo Client puede servir a varios targets si hiciera falta.
type Client struct {
	host   string
	client *http.Client
}

var _ ports.SyncClient = (*Client)(nil)

// Option configura un Client opcionalmente.
type Option func(*Client)

// WithHTTPClient inyecta un cliente HTTP propio (útil en tests o para ajustar el timeout).
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) { cl.client = c }
}

// NewClient crea el cliente contra el host dado (p. ej. http://localhost:8766). host vacío deja un
// cliente que fallará al llamar: la configuración decide si el sync está habilitado, no este
// constructor.
func NewClient(host string, opts ...Option) *Client {
	c := &Client{
		host:   strings.TrimRight(strings.TrimSpace(host), "/"),
		client: &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// wireChange es la representación de red de ports.Change. El payload viaja como JSON embebido
// (json.RawMessage), no como base64: el hub lo guarda opaco pero legible.
type wireChange struct {
	Key       string          `json:"key"`
	Op        string          `json:"op"`
	Payload   json.RawMessage `json:"payload"`
	UpdatedAt time.Time       `json:"updated_at"`
	Project   string          `json:"project,omitempty"`
}

// pushRequest es el cuerpo de POST .../changes.
type pushRequest struct {
	Changes []wireChange `json:"changes"`
}

// pullResponse es el cuerpo de la respuesta de GET .../changes.
type pullResponse struct {
	Changes []wireChange `json:"changes"`
	Cursor  int64        `json:"cursor"`
}

// Push envía las filas cambiadas al hub. Un lote vacío no viaja (no-op): el servicio ya filtra por la
// cola, pero lo defendemos aquí para no gastar un viaje de red.
func (c *Client) Push(ctx context.Context, ns, target string, changes []ports.Change) error {
	if len(changes) == 0 {
		return nil
	}
	body := pushRequest{Changes: make([]wireChange, len(changes))}
	for i, ch := range changes {
		body.Changes[i] = wireChange{
			Key:       ch.Key,
			Op:        string(ch.Op),
			Payload:   json.RawMessage(ch.Payload),
			UpdatedAt: ch.UpdatedAt,
			Project:   ch.Project,
		}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("httpsync: serializar push: %w", err)
	}

	endpoint := c.changesURL(ns, target)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("httpsync: construir push: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+target)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("httpsync: llamar a %s: %w", c.host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError("push", resp)
	}
	return nil
}

// Pull pide al hub las filas con server_seq > cursor y devuelve el nuevo cursor.
func (c *Client) Pull(ctx context.Context, ns, target string, cursor int64) ([]ports.Change, int64, error) {
	endpoint := c.changesURL(ns, target) + "?since=" + strconv.FormatInt(cursor, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("httpsync: construir pull: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+target)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("httpsync: llamar a %s: %w", c.host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, statusError("pull", resp)
	}

	var parsed pullResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, 0, fmt.Errorf("httpsync: parsear pull: %w", err)
	}
	changes := make([]ports.Change, len(parsed.Changes))
	for i, wc := range parsed.Changes {
		changes[i] = ports.Change{
			Key:       wc.Key,
			Op:        domain.SyncOp(wc.Op),
			Payload:   []byte(wc.Payload),
			UpdatedAt: wc.UpdatedAt,
			Project:   wc.Project,
		}
	}
	return changes, parsed.Cursor, nil
}

// changesURL arma la ruta /{ns}/{target}/changes escapando el target (es un token, puede traer
// caracteres que rompan la ruta).
func (c *Client) changesURL(ns, target string) string {
	return c.host + "/" + url.PathEscape(ns) + "/" + url.PathEscape(target) + "/changes"
}

// statusError construye un error legible a partir de una respuesta no-200, incluyendo el cuerpo si lo
// hay (el hub suele mandar un mensaje de error en texto).
func statusError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return fmt.Errorf("httpsync: %s: respuesta %s del hub", op, resp.Status)
	}
	return fmt.Errorf("httpsync: %s: respuesta %s del hub: %s", op, resp.Status, msg)
}
