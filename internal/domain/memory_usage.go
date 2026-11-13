package domain

import "time"

// MemoryUsage es la telemetría de uso de una memoria: cuántas veces se inyectó por relevancia y en
// cuántas de esas sesiones el agente acabó mencionándola. Es el lazo de realimentación sin el cual
// no se puede podar: guardar mucho sin saber qué se usa deja la memoria creciendo a ciegas.
//
// Los contadores InjectedCount y MentionedCount son TOTALES de por vida y nunca bajan. Para medir el
// efecto de una edición sin perder ese histórico existe una línea base (BaseInjected, BaseMentioned,
// BaselinedAt): una foto de los totales en el momento del corte. Lo ocurrido desde entonces es la
// diferencia (InjectedSince, MentionedSince). Sin línea base (cero, nil) el "desde" coincide con el
// total.
//
// Es un dato LOCAL de cada máquina, no viaja en el sync: los contadores de otra máquina no dicen
// nada sobre lo que pasa en esta.
type MemoryUsage struct {
	MemoryID        int64
	InjectedCount   int
	MentionedCount  int
	LastInjectedAt  *time.Time
	LastMentionedAt *time.Time

	BaseInjected  int        // InjectedCount en el instante de la línea base
	BaseMentioned int        // MentionedCount en el instante de la línea base
	BaselinedAt   *time.Time // cuándo se fijó la línea base; nil = nunca (el "desde" es el origen)
}

// InjectedSince devuelve las inyecciones ocurridas desde la línea base.
func (u MemoryUsage) InjectedSince() int { return u.InjectedCount - u.BaseInjected }

// MentionedSince devuelve las menciones ocurridas desde la línea base.
func (u MemoryUsage) MentionedSince() int { return u.MentionedCount - u.BaseMentioned }

// Dormant indica si la memoria, desde su línea base, se ha inyectado lo bastante como para juzgarla y
// nunca se mencionó. Es la señal de poda: peso constante en el contexto, cero rendimiento observable.
// El mínimo de inyecciones evita condenar a una memoria que solo salió una o dos veces por casualidad;
// medir desde la línea base evita condenar a una memoria ya reescrita por lo que hizo su versión vieja.
func (u MemoryUsage) Dormant(minInjections int) bool {
	return u.InjectedSince() >= minInjections && u.MentionedSince() == 0
}
