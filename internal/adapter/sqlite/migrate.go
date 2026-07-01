package sqlite

import "context"

// schema es el esquema declarativo del proyecto nuevo. Todo es idempotente (IF NOT EXISTS):
// correr migrate() sobre una base vacía o ya existente es seguro y no destruye datos.
//
// Diferencias deliberadas frente al proyecto viejo (ver §6.bis del plan):
//   - NO hay FTS5 (ni tabla virtual memories_fts ni sus triggers): la búsqueda deja de ser
//     léxica y pasa a ser 100% vectorial, sobre la tabla memory_vectors (vector BLOB 1:1 con
//     cada memoria; el coseno se calcula en Go puro, ver vector_index.go).
//   - Los timestamps se guardan como TEXT en formato "2006-01-02 15:04:05" (UTC), fijados por
//     la capa de servicio vía Clock; el adaptador no usa datetime('now') salvo en el borrado.
const schema = `
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

// migrate aplica el esquema declarativo. Idempotente: cada sentencia usa IF NOT EXISTS.
func (d *DB) migrate(ctx context.Context) error {
	_, err := d.sql.ExecContext(ctx, schema)
	return err
}
