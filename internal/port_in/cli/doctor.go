package cli

import (
	"context"
	"fmt"

	"github.com/javierv-dev/kandel/internal/diagnostic"
)

// doctorCmd corre los chequeos de salud de solo lectura e imprime el reporte. Sale con código
// distinto de cero si algún chequeo arroja un error (no por warnings).
type doctorCmd struct{}

func init() { register(doctorCmd{}) }

func (doctorCmd) Name() string     { return "doctor" }
func (doctorCmd) Synopsis() string { return "chequeos de salud de solo lectura" }

func (doctorCmd) Run(ctx context.Context, _ []string, app *App) error {
	report := app.Doctor.Run(ctx)
	for _, f := range report.Findings {
		fmt.Fprintf(app.Out, "[%s] %s: %s\n", f.Severity, f.Check, f.Message)
		if f.NextStep != "" {
			fmt.Fprintf(app.Out, "        → %s\n", f.NextStep)
		}
	}
	if report.OK() {
		fmt.Fprintln(app.Out, "\nTodo en orden.")
		return nil
	}
	fmt.Fprintf(app.Out, "\nEstado: %s\n", report.Worst)
	// Solo un error (no un warning) hace fallar el comando.
	if report.Worst == diagnostic.SeverityError {
		return fmt.Errorf("doctor: se encontraron problemas")
	}
	return nil
}
