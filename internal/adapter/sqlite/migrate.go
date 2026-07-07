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

// schemaV2 añade la garantía de idempotencia de la detección de conflictos: como mucho UNA relación
// juzgada por par de memorias, sin importar la dirección (A→B y B→A son el mismo conflicto). Antes,
// el barrido juzgaba el par desde ambos lados (detectOne(A) ve a B y detectOne(B) ve a A) y, sumado a
// mem_compare, acumulaba filas redundantes para el mismo par. Dos piezas:
//   - Un DELETE que colapsa las filas juzgadas duplicadas ya existentes, conservando la de menor id
//     (necesario: crear el índice único fallaría si quedaran duplicados). Solo borra redundancia real.
//   - Un índice ÚNICO PARCIAL sobre la clave canónica del par (menor||mayor de los sync_id), limitado a
//     las relaciones ya juzgadas (las pendientes son placeholders y no deben colisionar). Es la red de
//     seguridad a nivel de base: aunque dos caminos intenten insertar el mismo par, solo cuaja uno.
const schemaV2 = `
DELETE FROM memory_relations
WHERE judgment_status = 'judged' AND id NOT IN (
	SELECT MIN(id) FROM memory_relations
	WHERE judgment_status = 'judged'
	GROUP BY
		CASE WHEN source_id < target_id THEN source_id ELSE target_id END,
		CASE WHEN source_id < target_id THEN target_id ELSE source_id END
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_memrel_pair_judged ON memory_relations(
	CASE WHEN source_id < target_id THEN source_id ELSE target_id END,
	CASE WHEN source_id < target_id THEN target_id ELSE source_id END
) WHERE judgment_status = 'judged';
`

// schemaV3 añade el vector del TÍTULO a memory_vectors como una COLUMNA más, en la misma fila que el
// de contenido: ambos comparten memory_id, model y dimensions (salen del mismo embebedor en una sola
// llamada), así que no hace falta duplicarlos en otra tabla. Sirve a la detección de casi-duplicados
// (§20.E-ter): dos memorias que dicen lo mismo con otras palabras tienen coseno de CONTENIDO medio
// (no cruzan el corte) pero coseno de TÍTULO alto; el portón por título encuentra las candidatas que
// el de contenido pierde.
//   - Se renombra vector -> vector_content para nombres coherentes (queda sitio para vector_details).
//     RENAME COLUMN preserva los datos (no es destructivo); es seguro en SQLite moderno.
//   - vector_title es NULLABLE: las memorias previas quedan con NULL hasta que el backfill lo rellene,
//     y `vector_title IS NULL` es justamente el criterio de "falta el título" del backfill.
const schemaV3 = `
ALTER TABLE memory_vectors RENAME COLUMN vector TO vector_content;
ALTER TABLE memory_vectors ADD COLUMN vector_title BLOB;
`

// schemaV4 añade el cursor del sync por change-feed (modelo servidor, ver PLAN-SYNC-SERVIDOR.md §4.4).
// Cada máquina guarda un entero por destino (target): el mayor server_seq que ya aplicó del hub. Es la
// pieza que sustituye al conjunto sync_chunks del modelo viejo (chunk+manifest). Additiva: la tabla
// nace vacía y `cursor=0` es justamente el arranque en frío (baja el estado actual completo).
const schemaV4 = `
CREATE TABLE IF NOT EXISTS sync_cursor (
	target TEXT PRIMARY KEY,
	cursor INTEGER NOT NULL DEFAULT 0
);
`

// schemaV5 elimina sync_chunks, la tabla de contabilidad del modelo viejo (chunk+manifest), ya
// muerta tras pasar a change-feed (Fase 3). Es un DROP explícito y seguro, no una violación de la
// regla additiva: esa regla protege DATOS DE USUARIO, y sync_chunks solo guardaba metadatos
// regenerables (qué chunks se enviaron), sin memorias ni claves foráneas que la referencien. El plan
// (§21.C) admite drops precisamente por migración explícita como esta. Preferimos esquema limpio a
// dejar tablas huérfanas con nombres que confunden.
const schemaV5 = `
DROP TABLE IF EXISTS sync_chunks;
`

// schemaV6 añade la bandeja de conflictos de edición concurrente (§6.5 del plan de sync). Guarda la
// fila remota que NO se aplicó porque había una mutación local pendiente para la misma memoria, hasta
// que el usuario decida. Keyed por sync_id: una detección más nueva reemplaza a la anterior. Additiva.
const schemaV6 = `
CREATE TABLE IF NOT EXISTS sync_conflicts (
	sync_id     TEXT PRIMARY KEY,
	project     TEXT NOT NULL DEFAULT '',
	op          TEXT NOT NULL,
	payload     BLOB NOT NULL,
	incoming_at TEXT NOT NULL,
	detected_at TEXT NOT NULL
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
//   - **Preservar los datos de usuario:** la regla protege memorias/sesiones/etc., nunca un DROP que
//     pierda datos valiosos. Añadir tablas/columnas es lo normal; un rename es crear-copiar-swap.
//     Un DROP solo es admisible sobre una tabla MUERTA sin datos valiosos (p. ej. contabilidad
//     regenerable) y siempre por migración explícita (ver v5: elimina sync_chunks).
//   - **Append-only:** solo se AÑADEN entradas al final, con version = anterior + 1. Nunca se
//     edita una migración ya publicada: rompería las bases que ya la aplicaron.
//   - La v1 es el esquema base (§21): todo idempotente (IF NOT EXISTS), seguro sobre una base
//     recién creada o preexistente (user_version 0 => 1 sin perder datos).
var migrations = []migration{
	{version: 1, sql: schemaV1},
	{version: 2, sql: schemaV2},
	{version: 3, sql: schemaV3},
	{version: 4, sql: schemaV4},
	{version: 5, sql: schemaV5},
	{version: 6, sql: schemaV6},
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

// SchemaVersion expone la versión de esquema registrada en la base (PRAGMA user_version), para
// diagnóstico (p. ej. `kandel version`, §21 Eje 5). Es la versión ya migrada, no la declarada.
func (d *DB) SchemaVersion(ctx context.Context) (int, error) {
	return d.schemaVersion(ctx)
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
