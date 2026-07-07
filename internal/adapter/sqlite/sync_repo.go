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
func (r *SyncRepo) EnqueueMutation(ctx context.Context, m domain.SyncMutation) error {
	exec := execFromCtx(ctx, r.db)
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
	rows, err := execFromCtx(ctx, r.db).QueryContext(ctx, q, target, afterSeq, normalizeLimit(limit))
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
	if _, err := execFromCtx(ctx, r.db).ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("sqlite: confirmar mutaciones: %w", err)
	}
	return nil
}

// GetCursor devuelve el cursor del destino (mayor server_seq del hub ya aplicado); 0 si no hay fila.
func (r *SyncRepo) GetCursor(ctx context.Context, target string) (int64, error) {
	var cursor int64
	err := execFromCtx(ctx, r.db).QueryRowContext(ctx,
		`SELECT cursor FROM sync_cursor WHERE target = ?`, target).Scan(&cursor)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("sqlite: leer cursor: %w", err)
	}
	return cursor, nil
}

// SetCursor guarda (upsert) el cursor del destino tras aplicar un Pull.
func (r *SyncRepo) SetCursor(ctx context.Context, target string, cursor int64) error {
	const q = `INSERT INTO sync_cursor (target, cursor) VALUES (?, ?)
		ON CONFLICT (target) DO UPDATE SET cursor = excluded.cursor`
	if _, err := execFromCtx(ctx, r.db).ExecContext(ctx, q, target, cursor); err != nil {
		return fmt.Errorf("sqlite: guardar cursor: %w", err)
	}
	return nil
}

// RecordConflict guarda (upsert por sync_id) un conflicto de edición concurrente.
func (r *SyncRepo) RecordConflict(ctx context.Context, c domain.SyncConflict) error {
	const q = `INSERT INTO sync_conflicts (sync_id, project, op, payload, incoming_at, detected_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (sync_id) DO UPDATE SET
			project = excluded.project, op = excluded.op, payload = excluded.payload,
			incoming_at = excluded.incoming_at, detected_at = excluded.detected_at`
	if _, err := execFromCtx(ctx, r.db).ExecContext(ctx, q,
		c.SyncID, c.Project, string(c.IncomingOp), c.Incoming,
		formatTime(c.IncomingAt), occurredAtOrNow(c.DetectedAt),
	); err != nil {
		return fmt.Errorf("sqlite: registrar conflicto: %w", err)
	}
	return nil
}

// ListConflicts devuelve los conflictos pendientes; project vacío devuelve los de todos los proyectos.
func (r *SyncRepo) ListConflicts(ctx context.Context, project string) ([]domain.SyncConflict, error) {
	q := `SELECT sync_id, project, op, payload, incoming_at, detected_at FROM sync_conflicts`
	var args []any
	if project != "" {
		q += ` WHERE project = ?`
		args = append(args, project)
	}
	q += ` ORDER BY detected_at ASC`
	rows, err := execFromCtx(ctx, r.db).QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: listar conflictos: %w", err)
	}
	defer rows.Close()
	var out []domain.SyncConflict
	for rows.Next() {
		c, err := scanConflict(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetConflict devuelve el conflicto de una memoria, o nil si no hay.
func (r *SyncRepo) GetConflict(ctx context.Context, syncID string) (*domain.SyncConflict, error) {
	const q = `SELECT sync_id, project, op, payload, incoming_at, detected_at FROM sync_conflicts WHERE sync_id = ?`
	c, err := scanConflict(execFromCtx(ctx, r.db).QueryRowContext(ctx, q, syncID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: leer conflicto: %w", err)
	}
	return &c, nil
}

// DeleteConflict elimina el conflicto de una memoria (al resolverlo).
func (r *SyncRepo) DeleteConflict(ctx context.Context, syncID string) error {
	if _, err := execFromCtx(ctx, r.db).ExecContext(ctx, `DELETE FROM sync_conflicts WHERE sync_id = ?`, syncID); err != nil {
		return fmt.Errorf("sqlite: borrar conflicto: %w", err)
	}
	return nil
}

// CountConflicts cuenta los conflictos pendientes de un proyecto (vacío = todos).
func (r *SyncRepo) CountConflicts(ctx context.Context, project string) (int, error) {
	q := `SELECT COUNT(*) FROM sync_conflicts`
	var args []any
	if project != "" {
		q += ` WHERE project = ?`
		args = append(args, project)
	}
	var n int
	if err := execFromCtx(ctx, r.db).QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: contar conflictos: %w", err)
	}
	return n, nil
}

// scanConflict escanea una fila de sync_conflicts al modelo de dominio.
func scanConflict(sc rowScanner) (domain.SyncConflict, error) {
	var (
		c                      domain.SyncConflict
		op                     string
		incomingAt, detectedAt string
	)
	if err := sc.Scan(&c.SyncID, &c.Project, &op, &c.Incoming, &incomingAt, &detectedAt); err != nil {
		return domain.SyncConflict{}, err
	}
	c.IncomingOp = domain.SyncOp(op)
	var err error
	if c.IncomingAt, err = parseTime(incomingAt); err != nil {
		return domain.SyncConflict{}, err
	}
	if c.DetectedAt, err = parseTime(detectedAt); err != nil {
		return domain.SyncConflict{}, err
	}
	return c, nil
}

// AcquireLease intenta tomar el lease de un destino. Lo concede si está libre, vencido o ya es
// del mismo owner. La condición y el UPDATE van en una sola sentencia para ser atómicos.
func (r *SyncRepo) AcquireLease(ctx context.Context, target, owner string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = time.Minute
	}
	if _, err := execFromCtx(ctx, r.db).ExecContext(ctx,
		`INSERT OR IGNORE INTO sync_state (target_key, updated_at) VALUES (?, datetime('now'))`,
		target,
	); err != nil {
		return false, fmt.Errorf("sqlite: asegurar sync_state: %w", err)
	}
	until := formatTime(time.Now().UTC().Add(ttl))
	res, err := execFromCtx(ctx, r.db).ExecContext(ctx,
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
	if _, err := execFromCtx(ctx, r.db).ExecContext(ctx, q, target, owner); err != nil {
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
