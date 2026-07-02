// Package release implementa el port ports.ReleaseChecker (§21 Eje 6): consulta la última release
// publicada en GitHub y decide si es más nueva que el binario en ejecución. Es la ÚNICA llamada de
// red saliente de kandel; por eso es opcional (KANDEL_UPDATE_CHECK=off la desactiva), cachea el
// resultado un día para no golpear la API en cada arranque, y degrada en silencio ante cualquier
// fallo (offline, timeout, rate-limit): nunca rompe un flujo ni bloquea.
package release

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/javierv-dev/kandel/internal/config"
	"github.com/javierv-dev/kandel/internal/ports"
	"github.com/javierv-dev/kandel/internal/semver"
)

// Checker cumple el port de chequeo de versión.
var _ ports.ReleaseChecker = (*Checker)(nil)

// defaultOwner/defaultRepo apuntan al repositorio de kandel (del module path). Sobreescribibles por
// entorno por si el repositorio se mueve.
const (
	defaultOwner = "javierv-dev"
	defaultRepo  = "kandel"
	cacheTTL     = 24 * time.Hour
	httpTimeout  = 3 * time.Second
)

// cacheData es lo que se persiste entre invocaciones para respetar el rate-limit de GitHub.
type cacheData struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

// Checker consulta y cachea la última versión publicada. fetch y now son inyectables para pruebas.
type Checker struct {
	enabled   bool
	cachePath string
	ttl       time.Duration
	now       func() time.Time
	fetch     func(ctx context.Context) (string, error)
}

// NewChecker construye el Checker por defecto: habilitado salvo KANDEL_UPDATE_CHECK=off, con la
// caché en ~/.kandel/update-check.json y el fetch real contra la API de GitHub.
func NewChecker() *Checker {
	owner := envOr("KANDEL_UPDATE_OWNER", defaultOwner)
	repo := envOr("KANDEL_UPDATE_REPO", defaultRepo)
	return &Checker{
		enabled:   !strings.EqualFold(config.Resolve(config.UpdateCheck), "off"),
		cachePath: defaultCachePath(),
		ttl:       cacheTTL,
		now:       time.Now,
		fetch:     func(ctx context.Context) (string, error) { return githubLatest(ctx, owner, repo) },
	}
}

// Newer implementa ports.ReleaseChecker: devuelve la última versión conocida y si supera a running.
func (c *Checker) Newer(ctx context.Context, running string) (string, bool) {
	latest, ok := c.latest(ctx)
	if !ok {
		return "", false
	}
	return latest, semver.Less(running, latest)
}

// latest devuelve la última versión, de la caché si está fresca (< ttl) o de la red si no. ok es
// false si el chequeo está desactivado o no se pudo determinar. Tras cualquier intento de red se
// estampa CheckedAt (aunque falle) para no reintentar en cada comando ni añadir latencia al arranque
// cuando se está offline; en caso de fallo se conserva el último valor conocido si lo había.
func (c *Checker) latest(ctx context.Context) (string, bool) {
	if !c.enabled {
		return "", false
	}
	cache, _ := c.readCache()
	if !cache.CheckedAt.IsZero() && c.now().Sub(cache.CheckedAt) < c.ttl {
		return cache.LatestVersion, cache.LatestVersion != ""
	}
	latest, err := c.fetch(ctx)
	if err != nil {
		c.writeCache(cacheData{CheckedAt: c.now(), LatestVersion: cache.LatestVersion})
		return cache.LatestVersion, cache.LatestVersion != ""
	}
	c.writeCache(cacheData{CheckedAt: c.now(), LatestVersion: latest})
	return latest, latest != ""
}

func (c *Checker) readCache() (cacheData, error) {
	var data cacheData
	if c.cachePath == "" {
		return data, nil
	}
	raw, err := os.ReadFile(c.cachePath)
	if err != nil {
		return data, err
	}
	err = json.Unmarshal(raw, &data)
	return data, err
}

// writeCache persiste la caché. Best-effort: un fallo de escritura no es motivo para romper nada.
func (c *Checker) writeCache(data cacheData) {
	if c.cachePath == "" {
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.cachePath), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(c.cachePath, raw, 0o644)
}

// githubLatest consulta la última release publicada y devuelve su tag_name.
func githubLatest(ctx context.Context, owner, repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release: GitHub respondió %d", resp.StatusCode)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return strings.TrimSpace(body.TagName), nil
}

// defaultCachePath resuelve ~/.kandel/update-check.json (respeta KANDEL_DATA_DIR).
func defaultCachePath() string {
	dir, err := config.DefaultDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "update-check.json")
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
