package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// RelationRepo implementa ports.RelationRepo sobre SQLite. Las relaciones referencian a las
// memorias por su sync_id (portable entre máquinas), no por id local.
type RelationRepo struct {
	db execer
}

var _ ports.RelationRepo = (*RelationRepo)(nil)

// NewRelationRepo crea el repositorio de relaciones sobre la conexión dada.
func NewRelationRepo(db *DB) *RelationRepo {
	return &RelationRepo{db: db.sql}
}

// relationColumns es el orden único de columnas para leer una relación.
const relationColumns = `id, sync_id, source_id, target_id, relation, ifnull(reason, ''),
	ifnull(evidence, ''), confidence, judgment_status, created_at, updated_at`

// Insert crea una relación (típicamente pending) y devuelve su id local.
func (r *RelationRepo) Insert(ctx context.Context, rel domain.MemoryRelation) (int64, error) {
	const q = `INSERT INTO memory_relations
		(sync_id, source_id, target_id, relation, reason, evidence, confidence, judgment_status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := execFromCtx(ctx, r.db).ExecContext(ctx, q,
		rel.SyncID, rel.SourceID, rel.TargetID, string(rel.Kind),
		nullString(rel.Reason), nullString(rel.Evidence), rel.Confidence, string(rel.Status),
		formatTime(rel.CreatedAt), formatTime(rel.UpdatedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: insertar relación: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("sqlite: id de relación: %w", err)
	}
	return id, nil
}

// Get recupera una relación por id; (nil, nil) si no existe.
func (r *RelationRepo) Get(ctx context.Context, id int64) (*domain.MemoryRelation, error) {
	q := `SELECT ` + relationColumns + ` FROM memory_relations WHERE id = ?`
	rel, err := scanRelation(execFromCtx(ctx, r.db).QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: leer relación: %w", err)
	}
	return &rel, nil
}

// Judge aplica el veredicto del LLM a una relación: fija el verbo, el motivo y la confianza, y
// la marca como juzgada. El instante de actualización lo pone la base de datos.
func (r *RelationRepo) Judge(ctx context.Context, id int64, v domain.Verdict) error {
	const q = `UPDATE memory_relations
		SET relation = ?, reason = ?, confidence = ?, judgment_status = ?, updated_at = datetime('now')
		WHERE id = ?`
	_, err := execFromCtx(ctx, r.db).ExecContext(ctx, q,
		string(v.Relation), nullString(v.Reasoning), v.Confidence, string(domain.JudgmentJudged), id,
	)
	if err != nil {
		return fmt.Errorf("sqlite: juzgar relación: %w", err)
	}
	return nil
}

// ListByStatus lista relaciones por estado de juicio, de la más reciente a la más antigua.
func (r *RelationRepo) ListByStatus(ctx context.Context, status domain.JudgmentStatus, limit int) ([]domain.MemoryRelation, error) {
	q := `SELECT ` + relationColumns + ` FROM memory_relations
		WHERE judgment_status = ?
		ORDER BY datetime(created_at) DESC, id DESC LIMIT ?`
	rows, err := execFromCtx(ctx, r.db).QueryContext(ctx, q, string(status), normalizeLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("sqlite: consultar relaciones: %w", err)
	}
	defer rows.Close()
	var out []domain.MemoryRelation
	for rows.Next() {
		rel, err := scanRelation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	return out, rows.Err()
}

// scanRelation escanea una fila al modelo de dominio.
func scanRelation(sc rowScanner) (domain.MemoryRelation, error) {
	var (
		rel              domain.MemoryRelation
		kind, status     string
		createdAt, updAt string
	)
	if err := sc.Scan(
		&rel.ID, &rel.SyncID, &rel.SourceID, &rel.TargetID, &kind,
		&rel.Reason, &rel.Evidence, &rel.Confidence, &status, &createdAt, &updAt,
	); err != nil {
		return domain.MemoryRelation{}, err
	}
	rel.Kind = domain.RelationType(kind)
	rel.Status = domain.JudgmentStatus(status)
	var err error
	if rel.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.MemoryRelation{}, err
	}
	if rel.UpdatedAt, err = parseTime(updAt); err != nil {
		return domain.MemoryRelation{}, err
	}
	return rel, nil
}
