package domain

import "time"

// MemoryUsage es la telemetría de uso de una memoria: cuántas veces se inyectó por relevancia y en
// cuántas de esas sesiones el agente acabó mencionándola. Es el lazo de realimentación sin el cual
// no se puede podar: guardar mucho sin saber qué se usa deja la memoria creciendo a ciegas.
//
// Es un dato LOCAL de cada máquina, no viaja en el sync: los contadores de otra máquina no dicen
// nada sobre lo que pasa en esta.
type MemoryUsage struct {
	MemoryID        int64
	InjectedCount   int
	MentionedCount  int
	LastInjectedAt  *time.Time
	LastMentionedAt *time.Time
}

// Dormant indica si la memoria se ha inyectado lo bastante como para juzgarla y nunca se mencionó.
// Es la señal de poda: peso constante en el contexto, cero rendimiento observable. El mínimo de
// inyecciones evita condenar a una memoria que solo salió una o dos veces por casualidad.
func (u MemoryUsage) Dormant(minInjections int) bool {
	return u.InjectedCount >= minInjections && u.MentionedCount == 0
}
