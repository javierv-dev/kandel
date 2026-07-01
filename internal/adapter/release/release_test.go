package release

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// newTestChecker arma un Checker con caché en un temporal, reloj y fetch inyectados. fetchCalls
// cuenta cuántas veces se llamó al fetch, para verificar que la caché evita la red.
func newTestChecker(t *testing.T, fetch func() (string, error)) (*Checker, *int) {
	t.Helper()
	calls := 0
	c := &Checker{
		enabled:   true,
		cachePath: filepath.Join(t.TempDir(), "update-check.json"),
		ttl:       24 * time.Hour,
		now:       func() time.Time { return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) },
		fetch: func(context.Context) (string, error) {
			calls++
			return fetch()
		},
	}
	return c, &calls
}

func TestNewerFetchesAndCaches(t *testing.T) {
	c, calls := newTestChecker(t, func() (string, error) { return "v1.2.0", nil })

	latest, newer := c.Newer(context.Background(), "v1.0.0")
	if latest != "v1.2.0" || !newer {
		t.Fatalf("Newer = %q,%v; se esperaba v1.2.0,true", latest, newer)
	}
	// Segunda llamada dentro del TTL: usa la caché, no vuelve a la red.
	if _, _ = c.Newer(context.Background(), "v1.0.0"); *calls != 1 {
		t.Fatalf("la segunda llamada debe usar la caché; fetch corrió %d veces", *calls)
	}
}

func TestNewerNotNewerWhenUpToDate(t *testing.T) {
	c, _ := newTestChecker(t, func() (string, error) { return "v1.0.0", nil })
	latest, newer := c.Newer(context.Background(), "v1.0.0")
	if latest != "v1.0.0" || newer {
		t.Fatalf("estando al día: Newer = %q,%v; se esperaba v1.0.0,false", latest, newer)
	}
}

func TestDisabledSkipsCheck(t *testing.T) {
	c, calls := newTestChecker(t, func() (string, error) { return "v9.9.9", nil })
	c.enabled = false
	if latest, newer := c.Newer(context.Background(), "v1.0.0"); latest != "" || newer {
		t.Fatalf("desactivado no debe avisar: %q,%v", latest, newer)
	}
	if *calls != 0 {
		t.Fatalf("desactivado no debe tocar la red; fetch corrió %d veces", *calls)
	}
}

func TestFetchErrorNoPriorIsSilent(t *testing.T) {
	c, _ := newTestChecker(t, func() (string, error) { return "", errors.New("offline") })
	if latest, newer := c.Newer(context.Background(), "v1.0.0"); latest != "" || newer {
		t.Fatalf("sin red y sin caché previa debe callar: %q,%v", latest, newer)
	}
}

func TestFetchErrorKeepsLastKnown(t *testing.T) {
	// Primer intento OK → cachea v1.5.0.
	c, _ := newTestChecker(t, func() (string, error) { return "v1.5.0", nil })
	c.Newer(context.Background(), "v1.0.0")

	// Caduca la caché y ahora el fetch falla: debe conservar el último valor conocido.
	c.now = func() time.Time { return time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC) }
	c.fetch = func(context.Context) (string, error) { return "", errors.New("offline") }
	latest, newer := c.Newer(context.Background(), "v1.0.0")
	if latest != "v1.5.0" || !newer {
		t.Fatalf("tras fallo debe conservar el último conocido: %q,%v", latest, newer)
	}
}

func TestStaleCacheRefetches(t *testing.T) {
	c, calls := newTestChecker(t, func() (string, error) { return "v1.0.0", nil })
	c.Newer(context.Background(), "v1.0.0") // cachea con now = 2026-07-01

	// Avanza el reloj más allá del TTL y cambia lo que devuelve la red.
	c.now = func() time.Time { return time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC) }
	c.fetch = func(context.Context) (string, error) { *calls++; return "v2.0.0", nil }
	latest, newer := c.Newer(context.Background(), "v1.0.0")
	if latest != "v2.0.0" || !newer {
		t.Fatalf("con caché vencida debe reconsultar: %q,%v", latest, newer)
	}
}
