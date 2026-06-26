// Package diagnostic implementa los chequeos de salud de solo lectura de `kandel doctor`. Un
// Check inspecciona una parte del sistema y devuelve hallazgos (Finding); el Runner corre todos
// y arma un Report con el estado global. Nada aquí escribe ni repara: es diagnóstico puro, lo que
// lo hace seguro de ejecutar siempre y fácil de testear con dobles.
package diagnostic

import "context"

// Severity clasifica la gravedad de un hallazgo.
type Severity string

const (
	// SeverityInfo es informativo: no hay problema, o es algo que el usuario quizá quiera saber.
	SeverityInfo Severity = "info"
	// SeverityWarning indica un problema que degrada la experiencia pero no impide operar.
	SeverityWarning Severity = "warning"
	// SeverityError indica un fallo que rompe alguna funcionalidad.
	SeverityError Severity = "error"
)

// rank ordena las severidades para poder quedarse con la peor.
func (s Severity) rank() int {
	switch s {
	case SeverityError:
		return 2
	case SeverityWarning:
		return 1
	default:
		return 0
	}
}

// Finding es un hallazgo individual de un chequeo.
type Finding struct {
	Check    string   `json:"check"`               // nombre del chequeo que lo produjo
	Severity Severity `json:"severity"`            // gravedad
	Message  string   `json:"message"`             // qué se observó
	NextStep string   `json:"next_step,omitempty"` // acción sugerida (segura), si aplica
}

// Report es el resultado completo de correr los chequeos.
type Report struct {
	Findings []Finding `json:"findings"`
	Worst    Severity  `json:"worst"` // peor severidad encontrada (info si todo bien)
}

// OK indica si el sistema está sano (sin warnings ni errores).
func (r Report) OK() bool {
	return r.Worst == SeverityInfo
}

// Check es un chequeo de salud. Name lo identifica; Run inspecciona y devuelve sus hallazgos.
// Un chequeo que no encuentra problemas devuelve un Finding informativo (no una lista vacía), para
// que el reporte muestre que se ejecutó y salió bien.
type Check interface {
	Name() string
	Run(ctx context.Context) []Finding
}

// Runner corre un conjunto de chequeos y arma el reporte.
type Runner struct {
	checks []Check
}

// NewRunner construye el corredor con los chequeos dados.
func NewRunner(checks ...Check) Runner {
	return Runner{checks: checks}
}

// Run ejecuta todos los chequeos en orden y devuelve un reporte con todos los hallazgos y la peor
// severidad encontrada.
func (r Runner) Run(ctx context.Context) Report {
	report := Report{Worst: SeverityInfo}
	for _, check := range r.checks {
		for _, f := range check.Run(ctx) {
			report.Findings = append(report.Findings, f)
			if f.Severity.rank() > report.Worst.rank() {
				report.Worst = f.Severity
			}
		}
	}
	return report
}
