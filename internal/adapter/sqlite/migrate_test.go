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
