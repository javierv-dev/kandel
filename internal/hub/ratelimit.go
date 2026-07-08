package hub

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// maxBuckets acota el mapa de buckets: al superarlo se barren los inactivos antes de crear uno nuevo.
// Evita crecimiento ilimitado si llegan muchas IPs distintas (p. ej. un escaneo).
const maxBuckets = 10000

// bucketTTL es el tiempo sin actividad tras el cual un bucket se considera inactivo y se puede barrer.
const bucketTTL = 10 * time.Minute

// clientIP extrae la IP del cliente para el rate limit. Detrás de un reverse proxy de confianza en la
// misma máquina (Caddy/Nginx, como recomienda docs/deploy-hub.md) el RemoteAddr es el proxy; por eso se
// prefiere el primer salto de X-Forwarded-For y luego X-Real-IP, y solo si no hay proxy se cae a
// RemoteAddr. Supone que el proxy es de confianza: el hub escucha en loopback y solo el proxy le habla.
// Si el hub se expusiera directo a internet, estas cabeceras serían falsificables y el límite por IP
// perdería garantía (razón de más para no exponer el hub en claro).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return xr
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimiter es un token-bucket por IP en memoria. Reparte `rate` tokens por segundo con un tope de
// `burst`. Es defensa en profundidad contra el martilleo o la fuerza bruta sobre el token; la seguridad
// primaria es la entropía del token. Puro-Go, sin dependencias externas.
type rateLimiter struct {
	rate  float64 // tokens por segundo
	burst float64
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// newRateLimiter construye el limitador a partir de un límite en peticiones por minuto y una ráfaga
// máxima. now inyecta el reloj (nil = time.Now), para hacer los tests deterministas.
func newRateLimiter(perMinute, burst int, now func() time.Time) *rateLimiter {
	if now == nil {
		now = time.Now
	}
	return &rateLimiter{
		rate:    float64(perMinute) / 60.0,
		burst:   float64(burst),
		now:     now,
		buckets: map[string]*bucket{},
	}
}

// allow consume un token para la IP dada; devuelve false si no quedan tokens (la petición debe
// rechazarse con 429). Recarga el bucket en proporción al tiempo transcurrido desde el último acceso.
func (l *rateLimiter) allow(ip string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[ip]
	if !ok {
		if len(l.buckets) >= maxBuckets {
			l.sweep(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[ip] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens = min(l.burst, b.tokens+elapsed*l.rate)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep elimina los buckets inactivos (sin uso desde hace más de bucketTTL): su estado ya no aporta.
func (l *rateLimiter) sweep(now time.Time) {
	for ip, b := range l.buckets {
		if now.Sub(b.last) > bucketTTL {
			delete(l.buckets, ip)
		}
	}
}

// middleware envuelve un handler aplicando el límite por IP: si la IP agotó sus tokens, responde 429 y
// no llega al handler.
func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "demasiadas peticiones: espera un momento")
			return
		}
		next.ServeHTTP(w, r)
	})
}
