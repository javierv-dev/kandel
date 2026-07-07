package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/javierv-dev/kandel/internal/hub"
)

// HubStore implementa hub.Store sobre SQLite (puro-Go, CGO=0). Es el almacén del servidor de sync,
// separado por completo de la base de datos de memorias del cliente: guarda una fila por
// (ns, token, key) con una secuencia monótona server_seq por (ns, token). El payload es opaco (BLOB):
// el hub no interpreta el dominio. updated_at se guarda como nanosegundos Unix para comparar LWW sin
// depender del formato de texto.
type HubStore struct {
	db   *sql.DB
	keys keyedMutex // serializa escrituras por (ns, token) para que el bump de server_seq sea consistente
}

var _ hub.Store = (*HubStore)(nil)

// hubSchema crea la tabla de filas del hub. Genérica: no conoce qué es una memoria ni una skill.
const hubSchema = `
CREATE TABLE IF NOT EXISTS hub_changes (
    ns          TEXT    NOT NULL,
    token       TEXT    NOT NULL,
    key         TEXT    NOT NULL,
    server_seq  INTEGER NOT NULL,
    op          TEXT    NOT NULL,
    payload     BLOB    NOT NULL,
    updated_at  INTEGER NOT NULL,
    project     TEXT    NOT NULL DEFAULT '',
    received_at INTEGER NOT NULL,
    PRIMARY KEY (ns, token, key)
);
CREATE INDEX IF NOT EXISTS idx_hub_changes_seq ON hub_changes (ns, token, server_seq);
`

// OpenHubStore abre (o crea) la base de datos del hub en la ruta dada, aplica los PRAGMA y crea el
// esquema. ":memory:" da una base en memoria, útil en pruebas.
func OpenHubStore(ctx context.Context, path string) (*HubStore, error) {
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("hubstore: abrir %q: %w", path, err)
	}
	if err := sqldb.PingContext(ctx); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("hubstore: conectar %q: %w", path, err)
	}
	for _, p := range pragmas {
		if _, err := sqldb.ExecContext(ctx, p); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("hubstore: aplicar %q: %w", p, err)
		}
	}
	if _, err := sqldb.ExecContext(ctx, hubSchema); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("hubstore: crear esquema: %w", err)
	}
	return &HubStore{db: sqldb}, nil
}

// Close cierra la conexión subyacente.
func (s *HubStore) Close() error { return s.db.Close() }

// Apply aplica el lote con LWW por updated_at. Toma el lock de (ns, token) para leer el server_seq
// máximo una vez y asignar seqs crecientes a las filas efectivamente escritas, dentro de una
// transacción (o entra el lote entero o no entra).
func (s *HubStore) Apply(ctx context.Context, ns, token string, changes []hub.Change) error {
	if len(changes) == 0 {
		return nil
	}
	unlock := s.keys.lock(ns + "\x00" + token)
	defer unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("hubstore: abrir tx: %w", err)
	}
	defer tx.Rollback()

	seq, err := maxSeq(ctx, tx, ns, token)
	if err != nil {
		return err
	}

	now := time.Now().UTC().UnixNano()
	for _, ch := range changes {
		incoming := ch.UpdatedAt.UnixNano()
		var stored int64
		err := tx.QueryRowContext(ctx,
			`SELECT updated_at FROM hub_changes WHERE ns = ? AND token = ? AND key = ?`,
			ns, token, ch.Key).Scan(&stored)
		switch {
		case err == sql.ErrNoRows:
			// fila nueva: se inserta
		case err != nil:
			return fmt.Errorf("hubstore: leer fila %q: %w", ch.Key, err)
		case incoming <= stored:
			continue // LWW: lo guardado es más nuevo o igual → se ignora
		}
		seq++
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO hub_changes (ns, token, key, server_seq, op, payload, updated_at, project, received_at)
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
             ON CONFLICT (ns, token, key) DO UPDATE SET
                 server_seq  = excluded.server_seq,
                 op          = excluded.op,
                 payload     = excluded.payload,
                 updated_at  = excluded.updated_at,
                 project     = excluded.project,
                 received_at = excluded.received_at`,
			ns, token, ch.Key, seq, ch.Op, ch.Payload, incoming, ch.Project, now); err != nil {
			return fmt.Errorf("hubstore: escribir fila %q: %w", ch.Key, err)
		}
	}
	return tx.Commit()
}

// Since devuelve las filas con server_seq > cursor en orden ascendente y el nuevo cursor.
func (s *HubStore) Since(ctx context.Context, ns, token string, cursor int64) ([]hub.Change, int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, server_seq, op, payload, updated_at, project
           FROM hub_changes
          WHERE ns = ? AND token = ? AND server_seq > ?
          ORDER BY server_seq ASC`,
		ns, token, cursor)
	if err != nil {
		return nil, 0, fmt.Errorf("hubstore: consultar cambios: %w", err)
	}
	defer rows.Close()

	newCursor := cursor
	var out []hub.Change
	for rows.Next() {
		var (
			ch        hub.Change
			seq       int64
			updatedAt int64
		)
		if err := rows.Scan(&ch.Key, &seq, &ch.Op, &ch.Payload, &updatedAt, &ch.Project); err != nil {
			return nil, 0, fmt.Errorf("hubstore: escanear fila: %w", err)
		}
		ch.UpdatedAt = time.Unix(0, updatedAt).UTC()
		out = append(out, ch)
		if seq > newCursor {
			newCursor = seq
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("hubstore: recorrer filas: %w", err)
	}
	return out, newCursor, nil
}

// maxSeq devuelve el mayor server_seq guardado para (ns, token), o 0 si no hay filas.
func maxSeq(ctx context.Context, q execer, ns, token string) (int64, error) {
	var seq int64
	if err := q.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(server_seq), 0) FROM hub_changes WHERE ns = ? AND token = ?`,
		ns, token).Scan(&seq); err != nil {
		return 0, fmt.Errorf("hubstore: leer server_seq máximo: %w", err)
	}
	return seq, nil
}

// keyedMutex da un lock por clave (aquí, por (ns, token)) para serializar escrituras sin bloquear
// tokens distintos entre sí. Los locks se conservan en el mapa (el conjunto de tokens es acotado).
type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

// lock toma el lock de la clave y devuelve la función para liberarlo.
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	if k.m == nil {
		k.m = make(map[string]*sync.Mutex)
	}
	mu, ok := k.m[key]
	if !ok {
		mu = &sync.Mutex{}
		k.m[key] = mu
	}
	k.mu.Unlock()

	mu.Lock()
	return mu.Unlock
}
