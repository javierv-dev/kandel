package ports

import "context"

// ReleaseChecker averigua si hay una versión de kandel más nueva que la que corre (§21 Eje 6). Es
// un port para desacoplar a los consumidores (comando version, chequeo de diagnóstico, hook) del
// adaptador concreto que consulta el remoto y cachea. La implementación degrada en silencio: nunca
// devuelve error, y ante fallo/offline/desactivado deja latest vacío.
type ReleaseChecker interface {
	// Newer devuelve la última versión conocida y si es estrictamente más nueva que running.
	// latest vacío significa "no se pudo determinar" (sin red, desactivado o error): en ese caso
	// newer es false y el consumidor no debe avisar nada.
	Newer(ctx context.Context, running string) (latest string, newer bool)
}
