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

// FindJudgedPair devuelve la relación ya juzgada entre dos memorias (por sync_id), en cualquier
// dirección; (nil, nil) si no existe. Se apoya en idx_memrel_source/target para el filtro.
func (r *RelationRepo) FindJudgedPair(ctx context.Context, a, b string) (*domain.MemoryRelation, error) {
	q := `SELECT ` + relationColumns + ` FROM memory_relations
		WHERE judgment_status = 'judged'
		  AND ((source_id = ? AND target_id = ?) OR (source_id = ? AND target_id = ?))
		ORDER BY id LIMIT 1`
	rel, err := scanRelation(execFromCtx(ctx, r.db).QueryRowContext(ctx, q, a, b, b, a))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: buscar par juzgado: %w", err)
	}
	return &rel, nil
}

// CountOpenConflicts cuenta los conflictos reales sin resolver del proyecto: relaciones conflicts_with
// juzgadas cuyas dos memorias siguen vivas y donde al menos un lado pertenece al proyecto o es global.
func (r *RelationRepo) CountOpenConflicts(ctx context.Context, project string) (int, error) {
	const q = `SELECT COUNT(*) FROM memory_relations r
		JOIN memories s ON s.sync_id = r.source_id AND s.deleted_at IS NULL
		JOIN memories t ON t.sync_id = r.target_id AND t.deleted_at IS NULL
		WHERE r.relation = 'conflicts_with' AND r.judgment_status = 'judged'
		  AND (s.project = ? OR t.project = ? OR s.scope = 'global' OR t.scope = 'global')`
	var n int
	if err := execFromCtx(ctx, r.db).QueryRowContext(ctx, q, project, project).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlite: contar conflictos abiertos: %w", err)
	}
	return n, nil
}

// FindBySourceKind devuelve la relación juzgada con la source y el verbo dados; (nil, nil) si no hay.
// Sostiene el recorrido de la cadena de bitácora (verbo `continues`). Si hubiera varias, devuelve la
// más reciente. Se apoya en idx_memrel_source(source_id).
func (r *RelationRepo) FindBySourceKind(ctx context.Context, sourceSyncID string, kind domain.RelationType) (*domain.MemoryRelation, error) {
	q := `SELECT ` + relationColumns + ` FROM memory_relations
		WHERE source_id = ? AND relation = ? AND judgment_status = 'judged'
		ORDER BY datetime(created_at) DESC, id DESC LIMIT 1`
	rel, err := scanRelation(execFromCtx(ctx, r.db).QueryRowContext(ctx, q, sourceSyncID, string(kind)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: buscar relación por source y verbo: %w", err)
	}
	return &rel, nil
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
