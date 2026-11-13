package sqlite

import (
	"context"
	"fmt"
)

// Maintenance implementa cli.MaintenanceUseCases para el mantenimiento de la base de datos local.
// Expone operaciones puras de infraestructura: backup y purgas programadas, sin lógica de dominio.
type Maintenance struct {
	db *DB
}

// NewMaintenance construye el servicio de mantenimiento.
func NewMaintenance(db *DB) Maintenance {
	return Maintenance{db: db}
}

// Backup copia la base de datos al archivo de destino usando VACUUM INTO. Es seguro concurrir con
// lectores y escritores sin bloquear.
func (m Maintenance) Backup(ctx context.Context, destPath string) error {
	// VACUUM INTO no admite parámetros ?, hay que concatenar. Como destPath viene de la CLI,
	// se recomienda escapar o simplemente usar db.sql.ExecContext con el query formateado.
	// modernc.org/sqlite soporta VACUUM INTO.
	query := fmt.Sprintf("VACUUM INTO '%s'", destPath) // ¡Atención! destPath debe venir de origen confiable.
	// En sqlite escapamos las comillas simples de la ruta.
	escapedDestPath := ""
	for _, c := range destPath {
		if c == '\'' {
			escapedDestPath += "''"
		} else {
			escapedDestPath += string(c)
		}
	}
	query = fmt.Sprintf("VACUUM INTO '%s'", escapedDestPath)

	if _, err := m.db.sql.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("sqlite: backup a %q falló: %w", destPath, err)
	}
	return nil
}

// Sweep corre el mantenimiento periódico: trunca el WAL y purga mutaciones sincronizadas.
// Devuelve la cantidad de mutaciones purgadas.
func (m Maintenance) Sweep(ctx context.Context) (int, error) {
	// 1. Forzar un checkpoint del WAL completo.
	if _, err := m.db.sql.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return 0, fmt.Errorf("sqlite: checkpoint del WAL falló: %w", err)
	}

	// 2. Purgar sync_mutations que ya fueron confirmadas.
	// Retenemos las no confirmadas (acked_at IS NULL).
	res, err := m.db.sql.ExecContext(ctx, "DELETE FROM sync_mutations WHERE acked_at IS NOT NULL")
	if err != nil {
		return 0, fmt.Errorf("sqlite: purga de sync_mutations falló: %w", err)
	}
	purged, _ := res.RowsAffected()

	return int(purged), nil
}
