package sqlite

import (
	"context"
	"fmt"
)

// schemaV1 es el esquema base de kandel (línea base v1.0.0, §21 del plan). Todo es idempotente
// (IF NOT EXISTS): correr esta migración sobre una base vacía o ya existente es seguro y no
// destruye datos.
//
// Diferencias deliberadas frente al proyecto viejo (ver §6.bis del plan):
//   - NO hay FTS5 (ni tabla virtual memories_fts ni sus triggers): la búsqueda deja de ser
//     léxica y pasa a ser 100% vectorial, sobre la tabla memory_vectors (vector BLOB 1:1 con
//     cada memoria; el coseno se calcula en Go puro, ver vector_index.go).
//   - Los timestamps se guardan como TEXT en formato "2006-01-02 15:04:05" (UTC), fijados por
//     la capa de servicio vía Clock; el adaptador no usa datetime('now') salvo en el borrado.
const schemaV1 = `
CREATE TABLE IF NOT EXISTS sessions (
	id         TEXT PRIMARY KEY,
	project    TEXT NOT NULL,
	directory  TEXT NOT NULL,
	started_at TEXT NOT NULL,
	ended_at   TEXT,
	summary    TEXT
);

CREATE TABLE IF NOT EXISTS memories (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	sync_id         TEXT,
	session_id      TEXT,
	type            TEXT NOT NULL,
	title           TEXT NOT NULL,
	content         TEXT NOT NULL,
	tool_name       TEXT,
	project         TEXT,
	scope           TEXT NOT NULL DEFAULT 'project',
	normalized_hash TEXT,
	revision_count  INTEGER NOT NULL DEFAULT 1,
	duplicate_count INTEGER NOT NULL DEFAULT 1,
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL,
	last_seen_at    TEXT,
	resolution      TEXT NOT NULL DEFAULT '',
	conflict_checked INTEGER NOT NULL DEFAULT 0,
	deleted_at      TEXT,
	FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_mem_session ON memories(session_id);
CREATE INDEX IF NOT EXISTS idx_mem_type    ON memories(type);
CREATE INDEX IF NOT EXISTS idx_mem_project ON memories(project);
CREATE INDEX IF NOT EXISTS idx_mem_created ON memories(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mem_scope   ON memories(scope);
CREATE INDEX IF NOT EXISTS idx_mem_sync_id ON memories(sync_id);
CREATE INDEX IF NOT EXISTS idx_mem_deleted ON memories(deleted_at);
CREATE INDEX IF NOT EXISTS idx_mem_dedupe  ON memories(normalized_hash, project, scope, type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mem_resolution ON memories(resolution);

CREATE TABLE IF NOT EXISTS prompts (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	sync_id    TEXT,
	session_id TEXT,
	content    TEXT NOT NULL,
	project    TEXT,
	created_at TEXT NOT NULL,
	FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_prompts_session ON prompts(session_id);
CREATE INDEX IF NOT EXISTS idx_prompts_project ON prompts(project);
CREATE INDEX IF NOT EXISTS idx_prompts_created ON prompts(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_prompts_sync_id ON prompts(sync_id);

CREATE TABLE IF NOT EXISTS memory_relations (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	sync_id         TEXT NOT NULL UNIQUE,
	source_id       TEXT NOT NULL,
	target_id       TEXT NOT NULL,
	relation        TEXT NOT NULL DEFAULT 'pending',
	reason          TEXT,
	evidence        TEXT,
	confidence      REAL NOT NULL DEFAULT 0,
	judgment_status TEXT NOT NULL DEFAULT 'pending',
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_memrel_source ON memory_relations(source_id, judgment_status);
CREATE INDEX IF NOT EXISTS idx_memrel_target ON memory_relations(target_id, judgment_status);
CREATE INDEX IF NOT EXISTS idx_memrel_status ON memory_relations(judgment_status);

CREATE TABLE IF NOT EXISTS sync_state (
	target_key  TEXT PRIMARY KEY,
	lease_owner TEXT,
	lease_until TEXT,
	updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS sync_mutations (
	seq         INTEGER PRIMARY KEY AUTOINCREMENT,
	target_key  TEXT NOT NULL,
	entity      TEXT NOT NULL,
	entity_key  TEXT NOT NULL,
	op          TEXT NOT NULL,
	payload     TEXT NOT NULL,
	source      TEXT NOT NULL DEFAULT 'local',
	project     TEXT NOT NULL DEFAULT '',
	occurred_at TEXT NOT NULL DEFAULT (datetime('now')),
	acked_at    TEXT,
	FOREIGN KEY (target_key) REFERENCES sync_state(target_key)
);

CREATE INDEX IF NOT EXISTS idx_sync_mutations_pending ON sync_mutations(target_key, acked_at, seq);

CREATE TABLE IF NOT EXISTS sync_chunks (
	target_key  TEXT NOT NULL,
	chunk_id    TEXT NOT NULL,
	imported_at TEXT NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (target_key, chunk_id)
);

CREATE TABLE IF NOT EXISTS memory_vectors (
	memory_id  INTEGER PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
	model      TEXT NOT NULL,
	dimensions INTEGER NOT NULL,
	vector     BLOB NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
`

// migration es un paso de evolución del esquema, identificado por un número de versión
// estrictamente creciente. PRAGMA user_version registra hasta qué versión se ha migrado la base,
// de modo que cada migración corre una sola vez a lo largo de la vida de la instalación.
type migration struct {
	version int
	sql     string
}

// migrations es la lista ordenada de migraciones del esquema. REGLAS (§21.C del plan):
//   - **Additivas, nunca destructivas:** añadir tablas/columnas, jamás DROP/rename directo
//     (rename/drop => crear-copiar-swap en una migración explícita).
//   - **Append-only:** solo se AÑADEN entradas al final, con version = anterior + 1. Nunca se
//     edita una migración ya publicada: rompería las bases que ya la aplicaron.
//   - La v1 es el esquema base (§21): todo idempotente (IF NOT EXISTS), seguro sobre una base
//     recién creada o preexistente (user_version 0 => 1 sin perder datos).
var migrations = []migration{
	{version: 1, sql: schemaV1},
}

// migrate aplica, en orden, las migraciones con versión mayor que la registrada en la base
// (PRAGMA user_version), cada una en su propia transacción, y avanza user_version. Es seguro
// correrlo en cada arranque: las migraciones ya aplicadas se saltan y no destruye datos.
func (d *DB) migrate(ctx context.Context) error {
	current, err := d.schemaVersion(ctx)
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := d.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("migración v%d: %w", m.version, err)
		}
	}
	return nil
}

// schemaVersion lee la versión de esquema registrada en la base (0 en una base nueva).
func (d *DB) schemaVersion(ctx context.Context) (int, error) {
	var v int
	if err := d.sql.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("leer user_version: %w", err)
	}
	return v, nil
}

// applyMigration ejecuta el SQL de una migración y avanza user_version en una sola transacción:
// o se aplica entera o no se aplica nada (atomicidad ante fallos a mitad de camino).
func (d *DB) applyMigration(ctx context.Context, m migration) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	// PRAGMA no admite parámetros ligados; m.version es un int controlado por nosotros (no hay
	// riesgo de inyección).
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
		return err
	}
	return tx.Commit()
}
