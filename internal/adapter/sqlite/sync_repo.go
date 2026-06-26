package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// SyncRepo implementa ports.SyncRepo sobre SQLite: la cola de mutaciones, el registro de chunks
// sincronizados y los leases por destino. EnqueueMutation participa en la transacción que abrió
// la unidad de trabajo (vía el parámetro Tx), de modo que un cambio y su mutación se confirman
// o se revierten juntos.
type SyncRepo struct {
	db execer
}

var _ ports.SyncRepo = (*SyncRepo)(nil)

// NewSyncRepo crea el repositorio de sincronización sobre la conexión dada.
func NewSyncRepo(db *DB) *SyncRepo {
	return &SyncRepo{db: db.sql}
}

// EnqueueMutation añade una mutación a la cola dentro de la transacción dada. Garantiza primero
// que exista la fila de sync_state del destino (clave foránea de sync_mutations).
func (r *SyncRepo) EnqueueMutation(ctx context.Context, tx ports.Tx, m domain.SyncMutation) error {
	exec := execFromTx(tx, r.db)
	if _, err := exec.ExecContext(ctx,
		`INSERT OR IGNORE INTO sync_state (target_key, updated_at) VALUES (?, datetime('now'))`,
		m.TargetKey,
	); err != nil {
		return fmt.Errorf("sqlite: asegurar sync_state: %w", err)
	}
	const q = `INSERT INTO sync_mutations
		(target_key, entity, entity_key, op, payload, source, project, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := exec.ExecContext(ctx, q,
		m.TargetKey, string(m.Entity), m.EntityKey, string(m.Op),
		string(m.Payload), m.Source, m.Project, occurredAtOrNow(m.OccurredAt),
	)
	if err != nil {
		return fmt.Errorf("sqlite: encolar mutación: %w", err)
	}
	return nil
}

// ListPending devuelve las mutaciones sin confirmar de un destino, en orden de Seq, a partir de
// afterSeq (exclusivo).
func (r *SyncRepo) ListPending(ctx context.Context, target string, afterSeq int64, limit int) ([]domain.SyncMutation, error) {
	const q = `SELECT seq, target_key, entity, entity_key, op, payload, source, project, occurred_at, acked_at
		FROM sync_mutations
		WHERE target_key = ? AND acked_at IS NULL AND seq > ?
		ORDER BY seq ASC LIMIT ?`
	rows, err := r.db.QueryContext(ctx, q, target, afterSeq, normalizeLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("sqlite: listar pendientes: %w", err)
	}
	defer rows.Close()
	var out []domain.SyncMutation
	for rows.Next() {
		m, err := scanMutation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Ack confirma como replicadas las mutaciones con los Seq indicados.
func (r *SyncRepo) Ack(ctx context.Context, target string, seqs []int64) error {
	if len(seqs) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(seqs)), ",")
	q := `UPDATE sync_mutations SET acked_at = datetime('now')
		WHERE target_key = ? AND seq IN (` + placeholders + `)`
	args := make([]any, 0, len(seqs)+1)
	args = append(args, target)
	for _, s := range seqs {
		args = append(args, s)
	}
	if _, err := r.db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("sqlite: confirmar mutaciones: %w", err)
	}
	return nil
}

// RecordChunk marca un chunk como ya sincronizado con un destino (idempotente).
func (r *SyncRepo) RecordChunk(ctx context.Context, target, chunkID string) error {
	const q = `INSERT OR IGNORE INTO sync_chunks (target_key, chunk_id) VALUES (?, ?)`
	if _, err := r.db.ExecContext(ctx, q, target, chunkID); err != nil {
		return fmt.Errorf("sqlite: registrar chunk: %w", err)
	}
	return nil
}

// SyncedChunks devuelve el conjunto de chunks ya sincronizados con un destino.
func (r *SyncRepo) SyncedChunks(ctx context.Context, target string) (map[string]bool, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT chunk_id FROM sync_chunks WHERE target_key = ?`, target)
	if err != nil {
		return nil, fmt.Errorf("sqlite: leer chunks: %w", err)
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// AcquireLease intenta tomar el lease de un destino. Lo concede si está libre, vencido o ya es
// del mismo owner. La condición y el UPDATE van en una sola sentencia para ser atómicos.
func (r *SyncRepo) AcquireLease(ctx context.Context, target, owner string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = time.Minute
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO sync_state (target_key, updated_at) VALUES (?, datetime('now'))`,
		target,
	); err != nil {
		return false, fmt.Errorf("sqlite: asegurar sync_state: %w", err)
	}
	until := formatTime(time.Now().UTC().Add(ttl))
	res, err := r.db.ExecContext(ctx,
		`UPDATE sync_state
		 SET lease_owner = ?, lease_until = ?, updated_at = datetime('now')
		 WHERE target_key = ?
		   AND (lease_until IS NULL OR datetime(lease_until) <= datetime('now')
		        OR ifnull(lease_owner, '') = '' OR lease_owner = ?)`,
		owner, until, target, owner,
	)
	if err != nil {
		return false, fmt.Errorf("sqlite: adquirir lease: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ReleaseLease libera el lease de un destino si lo posee owner (o si está vacío).
func (r *SyncRepo) ReleaseLease(ctx context.Context, target, owner string) error {
	const q = `UPDATE sync_state
		SET lease_owner = NULL, lease_until = NULL, updated_at = datetime('now')
		WHERE target_key = ? AND (lease_owner = ? OR lease_owner IS NULL OR lease_owner = '')`
	if _, err := r.db.ExecContext(ctx, q, target, owner); err != nil {
		return fmt.Errorf("sqlite: liberar lease: %w", err)
	}
	return nil
}

// scanMutation escanea una fila de la cola al modelo de dominio.
func scanMutation(sc rowScanner) (domain.SyncMutation, error) {
	var (
		m          domain.SyncMutation
		entity, op string
		payload    string
		occurredAt string
		ackedAt    sql.NullString
	)
	if err := sc.Scan(
		&m.Seq, &m.TargetKey, &entity, &m.EntityKey, &op, &payload,
		&m.Source, &m.Project, &occurredAt, &ackedAt,
	); err != nil {
		return domain.SyncMutation{}, err
	}
	m.Entity = domain.SyncEntity(entity)
	m.Op = domain.SyncOp(op)
	m.Payload = []byte(payload)
	var err error
	if m.OccurredAt, err = parseTime(occurredAt); err != nil {
		return domain.SyncMutation{}, err
	}
	if ackedAt.Valid {
		t, err := parseTime(ackedAt.String)
		if err != nil {
			return domain.SyncMutation{}, err
		}
		m.AckedAt = &t
	}
	return m, nil
}

// occurredAtOrNow devuelve la representación TEXT del instante dado, o de "ahora" si es cero.
func occurredAtOrNow(t time.Time) string {
	if t.IsZero() {
		return formatTime(time.Now().UTC())
	}
	return formatTime(t)
}
