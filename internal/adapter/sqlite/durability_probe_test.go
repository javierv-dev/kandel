package sqlite

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

// --- Fase 0 de la auditoría de durabilidad (docs/auditoria-durabilidad.md) ---
//
// Estas pruebas MIDEN el estado real de los PRAGMA en las conexiones que abre el pool de
// database/sql. No afirman nada por defecto: su trabajo en la Fase 0 es producir la línea base que
// confirma o descarta las hipótesis H1 (PRAGMA aplicados al pool y no a cada conexión) y H2
// (synchronous nunca fijado de forma explícita).
//
// Por qué no fallan por defecto: si la hipótesis H1 es cierta, una aserción normal dejaría en rojo
// toda la suite antes de haber decidido siquiera la corrección, y la Definición de Hecho del
// proyecto exige `go test ./...` en verde. La aserción existe, pero detrás de la variable de
// entorno KANDEL_DURABILITY_STRICT=1: así el informe se obtiene siempre y el portón se activa
// cuando se aplique el arreglo.
//
//	go test ./internal/adapter/sqlite -run Durability -v                  # informe
//	KANDEL_DURABILITY_STRICT=1 go test ./internal/adapter/sqlite -run Durability   # portón

// pragmaSnapshot es el estado de durabilidad observado en UNA conexión concreta.
type pragmaSnapshot struct {
	journalMode string
	synchronous int
	foreignKeys int
	busyTimeout int
	walAutockpt int
}

// readPragmas lee el estado de durabilidad de la conexión dada. Se usa *sql.Conn (no *sql.DB) a
// propósito: es la única forma de garantizar que todas las lecturas van a la MISMA conexión física,
// que es justo lo que está en discusión.
func readPragmas(ctx context.Context, t *testing.T, c *sql.Conn) pragmaSnapshot {
	t.Helper()
	var s pragmaSnapshot
	queries := []struct {
		sql  string
		dest any
	}{
		{"PRAGMA journal_mode", &s.journalMode},
		{"PRAGMA synchronous", &s.synchronous},
		{"PRAGMA foreign_keys", &s.foreignKeys},
		{"PRAGMA busy_timeout", &s.busyTimeout},
		{"PRAGMA wal_autocheckpoint", &s.walAutockpt},
	}
	for _, q := range queries {
		if err := c.QueryRowContext(ctx, q.sql).Scan(q.dest); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	return s
}

// TestDurabilityPragmasAcrossPoolConnections abre varias conexiones SIMULTÁNEAS (mantenerlas vivas
// a la vez es lo que obliga al pool a crear conexiones físicas distintas en lugar de reutilizar
// una) y compara el estado de durabilidad de cada una contra la primera.
//
// Lo que mide: si `Open` aplica los PRAGMA con ExecContext sobre el *sql.DB, solo la conexión que
// atendió esa llamada queda configurada. journal_mode debería sobrevivir en todas (WAL se persiste
// en el archivo), pero foreign_keys y busy_timeout son estado POR CONEXIÓN y deberían aparecer en
// su valor por defecto (0) en las conexiones 2..N.
func TestDurabilityPragmasAcrossPoolConnections(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	const conns = 4
	held := make([]*sql.Conn, 0, conns)
	for i := 0; i < conns; i++ {
		c, err := db.sql.Conn(ctx)
		if err != nil {
			t.Fatalf("abrir conexión %d: %v", i, err)
		}
		// Mantenerlas todas vivas hasta el final del test: devolverlas al pool antes de tiempo
		// haría que la siguiente reutilizara la misma y el experimento no probaría nada.
		defer c.Close()
		held = append(held, c)
	}

	snaps := make([]pragmaSnapshot, len(held))
	for i, c := range held {
		snaps[i] = readPragmas(ctx, t, c)
	}

	t.Logf("conn | journal_mode | synchronous | foreign_keys | busy_timeout | wal_autockpt")
	for i, s := range snaps {
		t.Logf("  %2d | %12s | %11d | %12d | %12d | %12d",
			i, s.journalMode, s.synchronous, s.foreignKeys, s.busyTimeout, s.walAutockpt)
	}

	strict := os.Getenv("KANDEL_DURABILITY_STRICT") == "1"
	base := snaps[0]
	for i, s := range snaps[1:] {
		n := i + 1
		if s == base {
			continue
		}
		msg := "conexión %d difiere de la conexión 0: %+v vs %+v (hipótesis H1: los PRAGMA se " +
			"aplican al pool y no a cada conexión)"
		if strict {
			t.Errorf(msg, n, s, base)
			continue
		}
		t.Logf("HALLAZGO "+msg, n, s, base)
	}
}

// TestDurabilitySynchronousDefault deja registrado el valor de `synchronous` que el driver aplica
// cuando kandel no lo fija (hipótesis H2). 2 = FULL (un commit obliga fsync: seguro ante corte de
// energía), 1 = NORMAL (con WAL, un corte puede perder las últimas transacciones ya confirmadas),
// 0 = OFF. El test no juzga el valor: lo documenta, para que la Fase 2 mida el efecto real en vez
// de razonarlo sobre el papel.
func TestDurabilitySynchronousDefault(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	c, err := db.sql.Conn(ctx)
	if err != nil {
		t.Fatalf("abrir conexión: %v", err)
	}
	defer c.Close()

	s := readPragmas(ctx, t, c)
	nombres := map[int]string{0: "OFF", 1: "NORMAL", 2: "FULL", 3: "EXTRA"}
	nombre, ok := nombres[s.synchronous]
	if !ok {
		nombre = "desconocido"
	}
	t.Logf("synchronous por defecto = %d (%s), journal_mode = %s", s.synchronous, nombre, s.journalMode)
}

// TestDurabilityForeignKeysActuallyEnforced comprueba si la integridad referencial se aplica DE
// VERDAD sobre una conexión cualquiera del pool, y no solo si el PRAGMA dice que sí. Inserta una
// memoria con un session_id inexistente: con foreign_keys activo debe ser rechazada.
//
// Es la consecuencia observable de H1: el PRAGMA se declara en Open, pero si no llega a la conexión
// que atiende esta escritura, la fila huérfana entra sin protesta.
func TestDurabilityForeignKeysActuallyEnforced(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	// Agotar varias conexiones a la vez para que la escritura NO caiga en la conexión que
	// configuró Open, que es el caso interesante.
	for i := 0; i < 3; i++ {
		c, err := db.sql.Conn(ctx)
		if err != nil {
			t.Fatalf("abrir conexión %d: %v", i, err)
		}
		defer c.Close()
	}

	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO memories (session_id, type, title, content, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"sesion-que-no-existe", "discovery", "huérfana", "cuerpo",
		formatTime(baseTime), formatTime(baseTime),
	)

	strict := os.Getenv("KANDEL_DURABILITY_STRICT") == "1"
	if err == nil {
		const msg = "se insertó una memoria con session_id inexistente: la clave foránea NO se " +
			"está aplicando en esta conexión (hipótesis H1)"
		if strict {
			t.Error(msg)
			return
		}
		t.Log("HALLAZGO " + msg)
		return
	}
	t.Logf("la clave foránea sí se aplicó: %v", err)
}
