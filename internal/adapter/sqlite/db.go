// Package sqlite implementa los ports de persistencia sobre SQLite puro-Go
// (modernc.org/sqlite), conservando CGO_ENABLED=0 y el binario estático único. El SQL vive
// agrupado en este paquete (no disperso por el dominio) y cada repo cumple su interfaz de ports.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	// Driver SQLite puro-Go: registra el nombre "sqlite". Sin CGO.
	_ "modernc.org/sqlite"
)

// execer abstrae lo común entre *sql.DB y *sql.Tx, para que los repos ejecuten igual dentro o
// fuera de una transacción.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DB envuelve la conexión a SQLite ya abierta, configurada y migrada. Es el punto de entrada
// del adaptador: de aquí cuelgan los repos.
type DB struct {
	sql *sql.DB
}

// pragmas son los ajustes de conexión que se aplican al abrir. foreign_keys protege la
// integridad referencial; busy_timeout evita errores inmediatos por bloqueo; WAL mejora la
// concurrencia lectura/escritura sobre el archivo local.
var pragmas = []string{
	"PRAGMA foreign_keys = ON",
	"PRAGMA busy_timeout = 5000",
	"PRAGMA journal_mode = WAL",
}

// Open abre (o crea) la base de datos en la ruta dada, aplica los PRAGMA y corre las
// migraciones. Una ruta ":memory:" da una base en memoria, útil para pruebas.
func Open(ctx context.Context, path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: abrir %q: %w", path, err)
	}
	if err := sqldb.PingContext(ctx); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("sqlite: conectar %q: %w", path, err)
	}
	for _, p := range pragmas {
		if _, err := sqldb.ExecContext(ctx, p); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("sqlite: aplicar %q: %w", p, err)
		}
	}
	d := &DB{sql: sqldb}
	if err := d.migrate(ctx); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("sqlite: migrar: %w", err)
	}
	return d, nil
}

// Close cierra la conexión subyacente.
func (d *DB) Close() error {
	return d.sql.Close()
}
