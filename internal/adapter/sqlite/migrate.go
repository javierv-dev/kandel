package sqlite

import "context"

// schema es el esquema declarativo del proyecto nuevo. Todo es idempotente (IF NOT EXISTS):
// correr migrate() sobre una base vacía o ya existente es seguro y no destruye datos.
//
// Diferencias deliberadas frente al proyecto viejo (ver §6.bis del plan):
//   - NO hay FTS5 (ni tabla virtual observations_fts ni sus triggers): la búsqueda deja de ser
//     léxica y pasa a ser 100% vectorial. El motor vectorial (tabla observation_vectors) se
//     añadirá en la Fase 5.bis; esta migración cubre solo la Fase 3 (observaciones).
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

CREATE TABLE IF NOT EXISTS observations (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	sync_id         TEXT,
	session_id      TEXT NOT NULL,
	type            TEXT NOT NULL,
	title           TEXT NOT NULL,
	content         TEXT NOT NULL,
	tool_name       TEXT,
	project         TEXT,
	scope           TEXT NOT NULL DEFAULT 'project',
	topic_key       TEXT,
	normalized_hash TEXT,
	revision_count  INTEGER NOT NULL DEFAULT 1,
	duplicate_count INTEGER NOT NULL DEFAULT 1,
	pinned          INTEGER NOT NULL DEFAULT 0,
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL,
	last_seen_at    TEXT,
	review_after    TEXT,
	deleted_at      TEXT,
	FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_obs_session ON observations(session_id);
CREATE INDEX IF NOT EXISTS idx_obs_type    ON observations(type);
CREATE INDEX IF NOT EXISTS idx_obs_project ON observations(project);
CREATE INDEX IF NOT EXISTS idx_obs_created ON observations(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_obs_scope   ON observations(scope);
CREATE INDEX IF NOT EXISTS idx_obs_sync_id ON observations(sync_id);
CREATE INDEX IF NOT EXISTS idx_obs_topic   ON observations(topic_key, project, scope, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_obs_deleted ON observations(deleted_at);
CREATE INDEX IF NOT EXISTS idx_obs_dedupe  ON observations(normalized_hash, project, scope, type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_obs_review  ON observations(review_after);

CREATE TABLE IF NOT EXISTS user_prompts (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	sync_id    TEXT,
	session_id TEXT NOT NULL,
	content    TEXT NOT NULL,
	project    TEXT,
	created_at TEXT NOT NULL,
	FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_prompts_session ON user_prompts(session_id);
CREATE INDEX IF NOT EXISTS idx_prompts_project ON user_prompts(project);
CREATE INDEX IF NOT EXISTS idx_prompts_created ON user_prompts(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_prompts_sync_id ON user_prompts(sync_id);

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
`

// migrate aplica el esquema declarativo. Idempotente: cada sentencia usa IF NOT EXISTS.
func (d *DB) migrate(ctx context.Context) error {
	_, err := d.sql.ExecContext(ctx, schema)
	return err
}
