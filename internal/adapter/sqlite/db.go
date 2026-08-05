// Package sqlite implementa los ports de persistencia sobre SQLite puro-Go
// (modernc.org/sqlite), conservando CGO_ENABLED=0 y el binario estático único. El SQL vive
// agrupado en este paquete (no disperso por el dominio) y cada repo cumple su interfaz de ports.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

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

// pragmas son los ajustes de conexión que se aplican al abrir, en la sintaxis de parámetros de DSN
// de modernc.org/sqlite. foreign_keys protege la integridad referencial; busy_timeout hace que una
// escritura espere su turno en vez de rendirse ante un bloqueo; WAL mejora la concurrencia
// lectura/escritura sobre el archivo local.
//
// Van por el DSN y NO por un ExecContext posterior, y eso no es un detalle de estilo. foreign_keys
// y busy_timeout son estado POR CONEXIÓN: un ExecContext sobre el *sql.DB solo configura la
// conexión que lo atienda, y las que el pool abra después nacen sin ellos. El DSN, en cambio, se
// aplica a cada conexión nueva, incluidas las de la ventana de arranque (ping y migraciones).
//
// La auditoría de durabilidad midió lo que costaba la versión anterior: hasta 5 de 6 procesos
// fallaban al ABRIR la base con SQLITE_BUSY_RECOVERY, y con escrituras concurrentes dentro de un
// proceso —el caso del daemon, que atiende una goroutine por petición— se perdía entre el 50% (dos
// peticiones solapadas) y el 97% (sesenta) de los prompts. Ver docs/auditoria-durabilidad.md §5.
// _txlock=immediate merece explicación aparte, porque busy_timeout por sí solo NO basta y eso es
// contraintuitivo. database/sql abre las transacciones con BEGIN (diferido): la transacción toma
// primero un snapshot de LECTURA y solo pide el bloqueo de escritura al primer INSERT. Ante ese
// ascenso SQLite no puede hacer esperar al que llama —esperar con un snapshot tomado arriesga un
// interbloqueo—, así que devuelve SQLITE_BUSY de inmediato e ignora el busy_timeout. Con BEGIN
// IMMEDIATE la transacción pide el bloqueo de escritura desde el principio, y ahí sí se aplica la
// espera. Medido: con solo los PRAGMA se seguía perdiendo entre el 50% y el 90% de los prompts;
// añadiendo _txlock=immediate, ninguno.
var pragmas = []string{
	"_pragma=foreign_keys(1)",
	"_pragma=busy_timeout(5000)",
	"_pragma=journal_mode(WAL)",
	"_txlock=immediate",
}

// buildDSN compone el DSN de una ruta con los PRAGMA incorporados. Acepta rutas de Windows
// (separadores invertidos) y ":memory:"; si ya viene una URI "file:", solo le añade los parámetros.
func buildDSN(path string) string {
	params := strings.Join(pragmas, "&")

	// Una URI ya formada se respeta: solo se le anexan los parámetros con el separador que toque.
	if strings.HasPrefix(path, "file:") {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		return path + sep + params
	}
	if path == ":memory:" {
		return "file::memory:?" + params
	}
	// SQLite percent-decodifica la parte de ruta de una URI, así que hay que escapar lo que de otro
	// modo se interpretaría como delimitador (espacios, '?', '#'). Los dos puntos de la letra de
	// unidad en Windows se dejan tal cual: ahí no son delimitador.
	p := filepath.ToSlash(path)
	p = strings.ReplaceAll(p, "%", "%25")
	p = strings.ReplaceAll(p, "?", "%3f")
	p = strings.ReplaceAll(p, "#", "%23")
	p = strings.ReplaceAll(p, " ", "%20")
	return "file:" + p + "?" + params
}

// Open abre (o crea) la base de datos en la ruta dada, con los PRAGMA incorporados al DSN, y corre
// las migraciones. Una ruta ":memory:" da una base en memoria, útil para pruebas.
func Open(ctx context.Context, path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", buildDSN(path))
	if err != nil {
		return nil, fmt.Errorf("sqlite: abrir %q: %w", path, err)
	}
	if err := sqldb.PingContext(ctx); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("sqlite: conectar %q: %w", path, err)
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
