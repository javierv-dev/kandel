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
`

// migrate aplica el esquema declarativo. Idempotente: cada sentencia usa IF NOT EXISTS.
func (d *DB) migrate(ctx context.Context) error {
	_, err := d.sql.ExecContext(ctx, schema)
	return err
}
