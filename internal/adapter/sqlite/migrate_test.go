package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// latestVersion es la versión de esquema más alta declarada en migrations.
func latestVersion() int {
	v := 0
	for _, m := range migrations {
		if m.version > v {
			v = m.version
		}
	}
	return v
}

// TestMigrateFreshDBSetsLatestVersion verifica que abrir una base nueva deja user_version en la
// última versión declarada (la línea base y sus migraciones posteriores se aplican de una).
func TestMigrateFreshDBSetsLatestVersion(t *testing.T) {
	db := newTestDB(t)
	got, err := db.schemaVersion(context.Background())
	if err != nil {
		t.Fatalf("schemaVersion: %v", err)
	}
	if want := latestVersion(); got != want {
		t.Fatalf("user_version = %d, se esperaba %d", got, want)
	}
}

// TestMigrateIdempotentPreservesData verifica que reabrir una base ya migrada no falla, no cambia
// la versión y NO destruye datos existentes (la garantía central de §21.C: nunca más borrar la DB).
func TestMigrateIdempotentPreservesData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reopen.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (1): %v", err)
	}
	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO sessions (id, project, directory, started_at) VALUES (?, ?, ?, ?)`,
		"s1", "proj", "/dir", formatTime(baseTime),
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (2): %v", err)
	}
	t.Cleanup(func() { db2.Close() })

	got, err := db2.schemaVersion(ctx)
	if err != nil {
		t.Fatalf("schemaVersion: %v", err)
	}
	if want := latestVersion(); got != want {
		t.Fatalf("tras reabrir, user_version = %d, se esperaba %d", got, want)
	}

	var count int
	if err := db2.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE id = ?`, "s1").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("la sesión insertada se perdió al reabrir (count = %d)", count)
	}
}

// TestApplyMigrationAdditive verifica que una migración additiva se aplica en su transacción,
// avanza user_version y deja el cambio de esquema visible.
func TestApplyMigrationAdditive(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	next := latestVersion() + 1
	m := migration{version: next, sql: `ALTER TABLE sessions ADD COLUMN probe TEXT`}
	if err := db.applyMigration(ctx, m); err != nil {
		t.Fatalf("applyMigration: %v", err)
	}

	got, err := db.schemaVersion(ctx)
	if err != nil {
		t.Fatalf("schemaVersion: %v", err)
	}
	if got != next {
		t.Fatalf("user_version = %d, se esperaba %d", got, next)
	}

	// La columna nueva debe existir: un INSERT que la referencia no debe fallar.
	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO sessions (id, project, directory, started_at, probe) VALUES (?, ?, ?, ?, ?)`,
		"s2", "proj", "/dir", formatTime(baseTime), "ok",
	); err != nil {
		t.Fatalf("la columna additiva no quedó aplicada: %v", err)
	}
}

// TestApplyMigrationRollsBackOnError verifica la atomicidad: si el SQL de una migración falla, ni
// el esquema ni user_version quedan a medias.
func TestApplyMigrationRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	before, err := db.schemaVersion(ctx)
	if err != nil {
		t.Fatalf("schemaVersion: %v", err)
	}

	bad := migration{version: before + 1, sql: `THIS IS NOT VALID SQL`}
	if err := db.applyMigration(ctx, bad); err == nil {
		t.Fatal("se esperaba error de una migración con SQL inválido")
	}

	after, err := db.schemaVersion(ctx)
	if err != nil {
		t.Fatalf("schemaVersion: %v", err)
	}
	if after != before {
		t.Fatalf("user_version avanzó pese al fallo: antes %d, después %d", before, after)
	}
}

// TestMigrateV8PreservaLaTelemetriaExistente simula una base real en v7 (con contadores acumulados) y
// verifica que al abrirla con el binario nuevo la v8 conserva los totales y deja la línea base en
// "origen" (base 0, sin fecha): conducta idéntica a la anterior hasta que alguien la fije.
func TestMigrateV8PreservaLaTelemetriaExistente(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v7.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (1): %v", err)
	}
	id := addMemoryForUsage(t, db, "memoria con historia")
	if err := NewUsageRepo(db).RecordInjected(ctx, []int64{id, id, id}, baseTime); err != nil {
		t.Fatalf("RecordInjected: %v", err)
	}
	// Devuelve la base al esquema v7, retirando también los objetos introducidos después de v8.
	for _, stmt := range []string{
		`DROP TABLE handoff_resumptions`,
		`DROP TABLE handoff_checkpoints`,
		`ALTER TABLE sessions DROP COLUMN application`,
		`ALTER TABLE sessions DROP COLUMN conversation_id`,
		`ALTER TABLE memory_usage DROP COLUMN base_injected`,
		`ALTER TABLE memory_usage DROP COLUMN base_mentioned`,
		`ALTER TABLE memory_usage DROP COLUMN baselined_at`,
		`PRAGMA user_version = 7`,
	} {
		if _, err := db.sql.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("preparar base v7 (%s): %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open (2) debía migrar v7→v8: %v", err)
	}
	t.Cleanup(func() { db2.Close() })

	if v, _ := db2.schemaVersion(ctx); v != latestVersion() {
		t.Fatalf("user_version = %d, se esperaba %d", v, latestVersion())
	}
	got, err := NewUsageRepo(db2).Get(ctx, id)
	if err != nil {
		t.Fatalf("Get tras migrar: %v", err)
	}
	if got.InjectedCount != 3 || got.BaseInjected != 0 || got.BaselinedAt != nil {
		t.Errorf("tras migrar: total=%d base=%d baselined=%v, se esperaba 3, 0 y nil", got.InjectedCount, got.BaseInjected, got.BaselinedAt)
	}
	if got.InjectedSince() != 3 {
		t.Errorf("sin línea base el 'desde' debe coincidir con el total (3), fue %d", got.InjectedSince())
	}
}
