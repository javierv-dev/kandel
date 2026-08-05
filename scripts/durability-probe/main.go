// Command durability-probe informa el estado de durabilidad de una instalación viva de kandel.
// Es el instrumento de la Fase 0 de docs/auditoria-durabilidad.md.
//
// ABRE LA BASE EN SOLO-LECTURA. No migra, no escribe, no repara: una auditoría de durabilidad no
// puede arriesgar los datos que audita. Si algo requiere escritura, no es trabajo de esta sonda.
//
//	go run ./scripts/durability-probe              # usa ~/.kandel/kandel.db
//	go run ./scripts/durability-probe -db ruta.db  # una base concreta
//
// Con el servidor MCP corriendo la lectura puede fallar si el archivo -shm no es accesible; en ese
// caso cierra Claude Code y repite.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "durability-probe:", err)
		os.Exit(1)
	}
}

func run() error {
	dbFlag := flag.String("db", "", "ruta de la base a sondear; vacío = ~/.kandel/kandel.db")
	flag.Parse()

	path := *dbFlag
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolver el directorio del usuario: %w", err)
		}
		path = filepath.Join(home, ".kandel", "kandel.db")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no se encontró la base en %q: %w", path, err)
	}

	ctx := context.Background()
	db, err := openReadOnly(ctx, path)
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Printf("base: %s\n\n", path)
	reportFiles(path)
	if err := reportPragmas(ctx, db); err != nil {
		return err
	}
	if err := reportIntegrity(ctx, db); err != nil {
		return err
	}
	if err := reportCounts(ctx, db); err != nil {
		return err
	}
	return nil
}

// openReadOnly abre la base en modo solo-lectura. El PRAGMA query_only es un cinturón además de los
// tirantes: aunque el modo ro ya rechaza escrituras, deja constancia explícita de la intención.
func openReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("abrir %q en solo-lectura: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("conectar a %q en solo-lectura: %w "+
			"(¿hay un servidor MCP con la base abierta?)", path, err)
	}
	return db, nil
}

// reportFiles informa el tamaño del trío de archivos de SQLite. Un -wal que crece sin límite señala
// que nadie está haciendo checkpoint: los datos confirmados viven en el journal y no en la base.
func reportFiles(path string) {
	fmt.Println("## Archivos")
	for _, suf := range []string{"", "-wal", "-shm"} {
		p := path + suf
		nombre := filepath.Base(p)
		info, err := os.Stat(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fmt.Printf("  %-20s (no existe)\n", nombre)
				continue
			}
			fmt.Printf("  %-20s error: %v\n", nombre, err)
			continue
		}
		fmt.Printf("  %-20s %10d bytes  (%s)\n", nombre, info.Size(),
			info.ModTime().Format("2006-01-02 15:04:05"))
	}
	fmt.Println()
}

// reportPragmas vuelca el estado de durabilidad de la conexión. Ojo al interpretarlo: refleja ESTA
// conexión, no necesariamente las que abre kandel en producción (ver hipótesis H1); el estado por
// conexión del pool lo mide TestDurabilityPragmasAcrossPoolConnections.
func reportPragmas(ctx context.Context, db *sql.DB) error {
	fmt.Println("## PRAGMA (de esta conexión de solo-lectura)")
	pragmas := []string{
		"journal_mode", "synchronous", "foreign_keys", "busy_timeout",
		"wal_autocheckpoint", "user_version", "page_size", "page_count", "auto_vacuum",
	}
	for _, p := range pragmas {
		var v any
		if err := db.QueryRowContext(ctx, "PRAGMA "+p).Scan(&v); err != nil {
			fmt.Printf("  %-20s error: %v\n", p, err)
			continue
		}
		fmt.Printf("  %-20s %v\n", p, v)
	}
	fmt.Println()
	return nil
}

// reportIntegrity corre las dos verificaciones que SQLite ofrece sin escribir: integrity_check
// (estructura del archivo) y foreign_key_check (filas huérfanas). La segunda es la que importa aquí:
// si la hipótesis H1 es cierta, las claves foráneas llevan tiempo sin aplicarse y pueden haber
// entrado huérfanas que el esquema declara imposibles.
func reportIntegrity(ctx context.Context, db *sql.DB) error {
	fmt.Println("## Integridad")

	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check(20)")
	if err != nil {
		return fmt.Errorf("integrity_check: %w", err)
	}
	var problemas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return fmt.Errorf("integrity_check: %w", err)
		}
		problemas = append(problemas, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("integrity_check: %w", err)
	}
	if len(problemas) == 1 && problemas[0] == "ok" {
		fmt.Println("  integrity_check      ok")
	} else {
		fmt.Printf("  integrity_check      %d problema(s):\n", len(problemas))
		for _, p := range problemas {
			fmt.Println("    - " + p)
		}
	}

	fkRows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	defer fkRows.Close()
	huerfanas := map[string]int{}
	total := 0
	for fkRows.Next() {
		var tabla, padre string
		var rowid sql.NullInt64
		var fkid int
		if err := fkRows.Scan(&tabla, &rowid, &padre, &fkid); err != nil {
			return fmt.Errorf("foreign_key_check: %w", err)
		}
		huerfanas[tabla+" -> "+padre]++
		total++
	}
	if err := fkRows.Err(); err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	if total == 0 {
		fmt.Println("  foreign_key_check    ok (sin filas huérfanas)")
	} else {
		fmt.Printf("  foreign_key_check    %d fila(s) huérfana(s):\n", total)
		for rel, n := range huerfanas {
			fmt.Printf("    - %-40s %d\n", rel, n)
		}
	}
	fmt.Println()
	return nil
}

// conteo describe una cifra a reportar y la consulta que la produce.
type conteo struct {
	etiqueta string
	tabla    string
	query    string
}

// reportCounts informa el volumen de datos y, sobre todo, el retraso de la cola de sync: mutaciones
// encoladas que aún no salieron de esta máquina son datos que existen en un solo sitio.
func reportCounts(ctx context.Context, db *sql.DB) error {
	fmt.Println("## Volumen y retraso de sync")
	conteos := []conteo{
		{"memorias vivas", "memories", "SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL"},
		{"memorias borradas", "memories", "SELECT COUNT(*) FROM memories WHERE deleted_at IS NOT NULL"},
		{"memorias sin vector", "memory_vectors",
			`SELECT COUNT(*) FROM memories m LEFT JOIN memory_vectors v ON v.memory_id = m.id
			 WHERE m.deleted_at IS NULL AND v.vector_content IS NULL`},
		{"sesiones", "sessions", "SELECT COUNT(*) FROM sessions"},
		{"prompts", "prompts", "SELECT COUNT(*) FROM prompts"},
		{"relaciones", "memory_relations", "SELECT COUNT(*) FROM memory_relations"},
		// sync_mutations no se purga nunca: Ack marca acked_at, no borra. Contar el total mediría
		// el tamaño del log, no el atraso; lo pendiente es lo que aún vive en una sola máquina.
		{"mutaciones pendientes", "sync_mutations",
			"SELECT COUNT(*) FROM sync_mutations WHERE acked_at IS NULL"},
		{"mutaciones confirmadas", "sync_mutations",
			"SELECT COUNT(*) FROM sync_mutations WHERE acked_at IS NOT NULL"},
		{"conflictos de sync", "sync_conflicts", "SELECT COUNT(*) FROM sync_conflicts"},
	}
	for _, c := range conteos {
		existe, err := tableExists(ctx, db, c.tabla)
		if err != nil {
			return err
		}
		if !existe {
			fmt.Printf("  %-22s (la tabla %s no existe en este esquema)\n", c.etiqueta, c.tabla)
			continue
		}
		var n int
		if err := db.QueryRowContext(ctx, c.query).Scan(&n); err != nil {
			fmt.Printf("  %-22s error: %v\n", c.etiqueta, err)
			continue
		}
		fmt.Printf("  %-22s %d\n", c.etiqueta, n)
	}

	if existe, err := tableExists(ctx, db, "sync_cursor"); err != nil {
		return err
	} else if existe {
		rows, err := db.QueryContext(ctx, "SELECT target, cursor FROM sync_cursor ORDER BY target")
		if err != nil {
			return fmt.Errorf("sync_cursor: %w", err)
		}
		defer rows.Close()
		var cursores []string
		for rows.Next() {
			var target string
			var cursor int64
			if err := rows.Scan(&target, &cursor); err != nil {
				return fmt.Errorf("sync_cursor: %w", err)
			}
			cursores = append(cursores, fmt.Sprintf("%s=%d", target, cursor))
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("sync_cursor: %w", err)
		}
		if len(cursores) == 0 {
			fmt.Printf("  %-22s (sin destinos registrados)\n", "cursores de sync")
		} else {
			fmt.Printf("  %-22s %s\n", "cursores de sync", strings.Join(cursores, ", "))
		}
	}
	fmt.Println()
	return nil
}

// tableExists evita que la sonda reviente contra una base de un esquema más viejo o más nuevo que
// el que esta versión del código conoce.
func tableExists(ctx context.Context, db *sql.DB, nombre string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", nombre).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("consultar sqlite_master por %q: %w", nombre, err)
	}
	return n > 0, nil
}
