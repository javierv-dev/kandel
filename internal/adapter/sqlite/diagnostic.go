package sqlite

import (
	"context"
	"fmt"
	"os"

	"github.com/javierv-dev/kandel/internal/diagnostic"
)

// DatabaseCheck implementa diagnostic.Check para inspeccionar el estado de la base de datos
// principal: integridad, tamaño del WAL y retraso en la sincronización.
type DatabaseCheck struct {
	db *DB
}

// NewDatabaseCheck construye el chequeo.
func NewDatabaseCheck(db *DB) DatabaseCheck {
	return DatabaseCheck{db: db}
}

func (DatabaseCheck) Name() string { return "database" }

func (c DatabaseCheck) Run(ctx context.Context) []diagnostic.Finding {
	var findings []diagnostic.Finding
	ok := true

	// 1. Integridad
	var checkResult string
	err := c.db.sql.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&checkResult)
	if err != nil {
		findings = append(findings, diagnostic.Finding{
			Check:    c.Name(),
			Severity: diagnostic.SeverityError,
			Message:  fmt.Sprintf("no se pudo comprobar la integridad: %v", err),
		})
		ok = false
	} else if checkResult != "ok" {
		findings = append(findings, diagnostic.Finding{
			Check:    c.Name(),
			Severity: diagnostic.SeverityError,
			Message:  fmt.Sprintf("la base de datos está corrupta: %s", checkResult),
			NextStep: "restaurar desde un backup",
		})
		ok = false
	}

	// 2. Tamaño del WAL
	// Solo aplica si no es en memoria y no es una URI pura (sin path real extraíble).
	// Por simplicidad, si path es un archivo local.
	if c.db.path != ":memory:" && c.db.path != "" {
		// La ruta limpia ya debería estar en c.db.path, pero buildDSN escapa y pone file:
		// Aquí c.db.path es el argumento original de Open, que suele ser una ruta real (ej: ~/.kandel/kandel.db).
		walPath := c.db.path + "-wal"
		if stat, err := os.Stat(walPath); err == nil {
			sizeMB := float64(stat.Size()) / 1024 / 1024
			if sizeMB > 50 { // Límite arbitrario: un WAL de más de 50MB merece atención.
				findings = append(findings, diagnostic.Finding{
					Check:    c.Name(),
					Severity: diagnostic.SeverityWarning,
					Message:  fmt.Sprintf("el archivo WAL ha crecido mucho (%.1f MB)", sizeMB),
					NextStep: "kandel backup (forzará un checkpoint)",
				})
				ok = false
			}
		}
	}

	// 3. Cola de sincronización (mutaciones pendientes)
	var pending int
	err = c.db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_mutations WHERE acked_at IS NULL`).Scan(&pending)
	if err != nil {
		findings = append(findings, diagnostic.Finding{
			Check:    c.Name(),
			Severity: diagnostic.SeverityError,
			Message:  fmt.Sprintf("no se pudo contar las mutaciones pendientes: %v", err),
		})
		ok = false
	} else if pending > 50 { // Si hay muchas pendientes, hay un problema en el demonio o conexión.
		findings = append(findings, diagnostic.Finding{
			Check:    c.Name(),
			Severity: diagnostic.SeverityWarning,
			Message:  fmt.Sprintf("hay %d mutaciones pendientes de sincronizar", pending),
			NextStep: "verificar conexión a red o revisar logs del demonio",
		})
		ok = false
	}

	if ok {
		findings = append(findings, diagnostic.Finding{
			Check:    c.Name(),
			Severity: diagnostic.SeverityInfo,
			Message:  "íntegra y al día",
		})
	}

	return findings
}
