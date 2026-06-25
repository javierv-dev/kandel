package ports

import "time"

// Clock abstrae la lectura del tiempo. Se inyecta para que las reglas que dependen de "ahora"
// (decaimiento, leases, marcas de tiempo) sean deterministas en los tests, eliminando las
// llamadas ocultas a time.Now().
type Clock interface {
	// Now devuelve el instante actual.
	Now() time.Time
}
