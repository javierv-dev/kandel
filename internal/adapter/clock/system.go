// Package clock provee implementaciones de ports.Clock. La de sistema lee el reloj real;
// en los tests se usa un reloj fijo para hacer deterministas las reglas que dependen del tiempo.
package clock

import (
	"time"

	"github.com/javierv-dev/kandel/internal/ports"
)

// System implementa ports.Clock leyendo el reloj real, siempre en UTC.
type System struct{}

// Aserción en tiempo de compilación.
var _ ports.Clock = System{}

// Now devuelve el instante actual en UTC.
func (System) Now() time.Time {
	return time.Now().UTC()
}
