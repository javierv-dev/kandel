package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// memStore es un hub.Store mínimo para las pruebas del handler: no guarda nada y sirve un feed vacío.
type memStore struct{}

func newMemStore() *memStore { return &memStore{} }

func (*memStore) Apply(context.Context, string, string, []Change) error { return nil }
func (*memStore) Since(_ context.Context, _, _ string, cursor int64) ([]Change, int64, error) {
	return nil, cursor, nil
}

func TestRateLimiterBurstThenRefill(t *testing.T) {
	now := time.Unix(1700000000, 0)
	clock := func() time.Time { return now }
	// 60 req/min = 1 token/seg, ráfaga de 3.
	l := newRateLimiter(60, 3, clock)

	// La ráfaga inicial permite 3 y rechaza la 4.ª sin que pase tiempo.
	for i := 0; i < 3; i++ {
		if !l.allow("1.2.3.4") {
			t.Fatalf("la petición %d de la ráfaga debía permitirse", i+1)
		}
	}
	if l.allow("1.2.3.4") {
		t.Fatal("la 4.ª petición debía rechazarse (ráfaga agotada)")
	}

	// Otra IP tiene su propio bucket: no la afecta la primera.
	if !l.allow("9.9.9.9") {
		t.Fatal("una IP distinta debe tener su propia cuota")
	}

	// Tras 1 segundo se recarga 1 token → una petición más permitida, la siguiente no.
	now = now.Add(time.Second)
	if !l.allow("1.2.3.4") {
		t.Fatal("tras 1s debía recargarse 1 token")
	}
	if l.allow("1.2.3.4") {
		t.Fatal("solo se recargó 1 token: la siguiente debía rechazarse")
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		xreal      string
		want       string
	}{
		{"remote sin proxy", "203.0.113.7:5555", "", "", "203.0.113.7"},
		{"x-forwarded-for primer salto", "127.0.0.1:80", "198.51.100.9, 10.0.0.1", "", "198.51.100.9"},
		{"x-real-ip", "127.0.0.1:80", "", "198.51.100.22", "198.51.100.22"},
		{"xff gana a x-real-ip", "127.0.0.1:80", "198.51.100.9", "10.0.0.1", "198.51.100.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = c.remoteAddr
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if c.xreal != "" {
				r.Header.Set("X-Real-IP", c.xreal)
			}
			if got := clientIP(r); got != c.want {
				t.Errorf("clientIP = %q, se esperaba %q", got, c.want)
			}
		})
	}
}

func TestHandlerRateLimitReturns429(t *testing.T) {
	store := newMemStore()
	// 60 req/min, ráfaga 2: la 3.ª petición seguida de la misma IP debe dar 429.
	h := Handler(store, WithRateLimit(60, 2))

	do := func() int {
		r := httptest.NewRequest(http.MethodGet, "/memory/tok/changes?since=0", nil)
		r.RemoteAddr = "203.0.113.50:4000"
		r.Header.Set("Authorization", "Bearer tok")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	if code := do(); code != http.StatusOK {
		t.Fatalf("1.ª petición: se esperaba 200, fue %d", code)
	}
	if code := do(); code != http.StatusOK {
		t.Fatalf("2.ª petición: se esperaba 200, fue %d", code)
	}
	if code := do(); code != http.StatusTooManyRequests {
		t.Fatalf("3.ª petición: se esperaba 429, fue %d", code)
	}
}
