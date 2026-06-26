package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// ObservationRepo implementa ports.ObservationRepo sobre SQLite. Es persistencia pura: no
// decide dedupe ni topic_key (eso vive en service/memory). Aquí solo se guarda y se recupera;
// los métodos de consulta (FindByHash, LatestByTopicKey) le dan al servicio lo que necesita
// para tomar esas decisiones.
type ObservationRepo struct {
	db execer
}

// Aserción en tiempo de compilación: el repo cumple el contrato del port.
var _ ports.ObservationRepo = (*ObservationRepo)(nil)

// NewObservationRepo crea el repositorio sobre la conexión dada.
func NewObservationRepo(db *DB) *ObservationRepo {
	return &ObservationRepo{db: db.sql}
}

// Add inserta una observación nueva con los valores ya resueltos por el servicio (incluidos
// los timestamps). Devuelve el id local autogenerado.
func (r *ObservationRepo) Add(ctx context.Context, o domain.Observation) (int64, error) {
	const q = `
		INSERT INTO observations (
			sync_id, session_id, type, title, content, tool_name, project, scope,
			topic_key, normalized_hash, revision_count, duplicate_count, pinned,
			created_at, updated_at, last_seen_at, review_after, deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := r.db.ExecContext(ctx, q,
		nullString(o.SyncID), o.SessionID, string(o.Type), o.Title, o.Content,
		nullString(o.ToolName), nullString(o.Project), string(o.Scope),
		nullString(string(o.TopicKey)), nullString(o.NormalizedHash),
		o.RevisionCount, o.DuplicateCount, boolToInt(o.Pinned),
		formatTime(o.CreatedAt), formatTime(o.UpdatedAt),
		nullTime(o.LastSeenAt), nullTime(o.ReviewAfter), nullTime(deletedAtValue(o)),
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: insertar observación: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("sqlite: id de observación: %w", err)
	}
	return id, nil
}

// Get recupera una observación viva por id; devuelve (nil, nil) si no existe.
func (r *ObservationRepo) Get(ctx context.Context, id int64) (*domain.Observation, error) {
	q := `SELECT ` + observationColumns + ` FROM observations WHERE id = ? AND deleted_at IS NULL`
	return r.queryOne(ctx, q, id)
}

// GetBySyncID recupera por identidad estable de sync; (nil, nil) si no existe.
func (r *ObservationRepo) GetBySyncID(ctx context.Context, syncID string) (*domain.Observation, error) {
	q := `SELECT ` + observationColumns + `
		FROM observations WHERE sync_id = ? AND deleted_at IS NULL
		ORDER BY id DESC LIMIT 1`
	return r.queryOne(ctx, q, syncID)
}

// Update reemplaza los campos editables de una observación y devuelve su estado final. Los
// timestamps (updated_at) los fija el servicio en la entidad recibida.
func (r *ObservationRepo) Update(ctx context.Context, id int64, o domain.Observation) (*domain.Observation, error) {
	const q = `
		UPDATE observations SET
			type = ?, title = ?, content = ?, tool_name = ?, project = ?, scope = ?,
			topic_key = ?, normalized_hash = ?, revision_count = ?, duplicate_count = ?,
			updated_at = ?, last_seen_at = ?, review_after = ?
		WHERE id = ? AND deleted_at IS NULL`
	res, err := r.db.ExecContext(ctx, q,
		string(o.Type), o.Title, o.Content, nullString(o.ToolName), nullString(o.Project),
		string(o.Scope), nullString(string(o.TopicKey)), nullString(o.NormalizedHash),
		o.RevisionCount, o.DuplicateCount,
		formatTime(o.UpdatedAt), nullTime(o.LastSeenAt), nullTime(o.ReviewAfter),
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: actualizar observación: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return r.Get(ctx, id)
}

// SoftDelete marca la observación como borrada sin quitarla. La marca de tiempo la pone la base
// de datos (datetime('now')): es un dato de auditoría, no una regla de dominio.
func (r *ObservationRepo) SoftDelete(ctx context.Context, id int64) error {
	const q = `UPDATE observations SET deleted_at = datetime('now') WHERE id = ? AND deleted_at IS NULL`
	if _, err := r.db.ExecContext(ctx, q, id); err != nil {
		return fmt.Errorf("sqlite: soft-delete observación: %w", err)
	}
	return nil
}

// HardDelete elimina físicamente la observación.
func (r *ObservationRepo) HardDelete(ctx context.Context, id int64) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM observations WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: hard-delete observación: %w", err)
	}
	return nil
}

// Recent lista observaciones vivas por recencia (camino de navegación del usuario).
func (r *ObservationRepo) Recent(ctx context.Context, q ports.RecentQuery) ([]domain.Observation, error) {
	query := `SELECT ` + observationColumns + ` FROM observations WHERE deleted_at IS NULL`
	args := []any{}
	if q.Project != "" {
		query += ` AND project = ?`
		args = append(args, q.Project)
	}
	if q.Scope != "" {
		query += ` AND scope = ?`
		args = append(args, string(q.Scope))
	}
	query += ` ORDER BY datetime(created_at) DESC, id DESC LIMIT ?`
	args = append(args, normalizeLimit(q.Limit))
	return r.queryMany(ctx, query, args...)
}

// Search hidrata a SearchResult los vecinos resueltos por el VectorIndex, preservando el orden
// de cercanía y su puntuación, y aplicando los filtros de q (tipo/proyecto/alcance). Las
// observaciones borradas o que no pasen los filtros se descartan.
func (r *ObservationRepo) Search(ctx context.Context, q ports.SearchQuery, neighbors []ports.Neighbor) ([]domain.SearchResult, error) {
	if len(neighbors) == 0 {
		return nil, nil
	}
	// Mapa id→score para reconstruir orden y puntuación tras leer las filas (que vuelven en
	// orden arbitrario de la base de datos).
	scoreByID := make(map[int64]float64, len(neighbors))
	ids := make([]any, 0, len(neighbors))
	for _, n := range neighbors {
		scoreByID[n.ObservationID] = n.Score
		ids = append(ids, n.ObservationID)
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	query := `SELECT ` + observationColumns + ` FROM observations
		WHERE deleted_at IS NULL AND id IN (` + placeholders + `)`
	args := append([]any{}, ids...)
	if q.Type != "" {
		query += ` AND type = ?`
		args = append(args, string(q.Type))
	}
	if q.Project != "" {
		query += ` AND project = ?`
		args = append(args, q.Project)
	}
	if q.Scope != "" {
		query += ` AND scope = ?`
		args = append(args, string(q.Scope))
	}

	found, err := r.queryMany(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]domain.Observation, len(found))
	for _, o := range found {
		byID[o.ID] = o
	}

	// Recorrer los vecinos en su orden original (de más a menos cercano) y emitir solo los que
	// sobrevivieron a los filtros, adjuntando su puntuación.
	limit := normalizeLimit(q.Limit)
	out := make([]domain.SearchResult, 0, len(neighbors))
	for _, n := range neighbors {
		o, ok := byID[n.ObservationID]
		if !ok {
			continue
		}
		out = append(out, domain.SearchResult{Observation: o, Score: scoreByID[n.ObservationID]})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// FindByHash devuelve la observación viva más reciente con el mismo hash normalizado dentro del
// mismo proyecto y alcance (base del dedupe exacto). (nil, nil) si no hay ninguna.
func (r *ObservationRepo) FindByHash(ctx context.Context, project string, scope domain.Scope, hash string) (*domain.Observation, error) {
	q := `SELECT ` + observationColumns + ` FROM observations
		WHERE normalized_hash = ?
		  AND ifnull(project, '') = ?
		  AND scope = ?
		  AND deleted_at IS NULL
		ORDER BY datetime(created_at) DESC, id DESC LIMIT 1`
	return r.queryOne(ctx, q, hash, project, string(scope))
}

// LatestByTopicKey devuelve la observación viva más reciente de un mismo tema dentro del mismo
// proyecto y alcance (base de la fusión por topic_key). (nil, nil) si no hay ninguna.
func (r *ObservationRepo) LatestByTopicKey(ctx context.Context, project string, scope domain.Scope, key domain.TopicKey) (*domain.Observation, error) {
	q := `SELECT ` + observationColumns + ` FROM observations
		WHERE topic_key = ?
		  AND ifnull(project, '') = ?
		  AND scope = ?
		  AND deleted_at IS NULL
		ORDER BY datetime(updated_at) DESC, datetime(created_at) DESC LIMIT 1`
	return r.queryOne(ctx, q, string(key), project, string(scope))
}

// SetPinned fija o desfija una observación (estado local).
func (r *ObservationRepo) SetPinned(ctx context.Context, id int64, pinned bool) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE observations SET pinned = ? WHERE id = ?`, boolToInt(pinned), id); err != nil {
		return fmt.Errorf("sqlite: fijar observación: %w", err)
	}
	return nil
}

// NeedingReview lista observaciones vivas cuya fecha de revisión ya venció (review_after en el
// pasado), de la más antigua a la más nueva.
func (r *ObservationRepo) NeedingReview(ctx context.Context, project string, limit int) ([]domain.Observation, error) {
	query := `SELECT ` + observationColumns + ` FROM observations
		WHERE deleted_at IS NULL
		  AND review_after IS NOT NULL
		  AND datetime(review_after) < datetime('now')`
	args := []any{}
	if project != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}
	query += ` ORDER BY datetime(review_after) ASC LIMIT ?`
	args = append(args, normalizeLimit(limit))
	return r.queryMany(ctx, query, args...)
}

// ─── helpers internos ────────────────────────────────────────────────────────

// queryOne ejecuta una consulta que devuelve a lo sumo una fila; (nil, nil) si no hay ninguna.
func (r *ObservationRepo) queryOne(ctx context.Context, query string, args ...any) (*domain.Observation, error) {
	o, err := scanObservation(r.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: leer observación: %w", err)
	}
	return &o, nil
}

// queryMany ejecuta una consulta que devuelve varias filas.
func (r *ObservationRepo) queryMany(ctx context.Context, query string, args ...any) ([]domain.Observation, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: consultar observaciones: %w", err)
	}
	return scanObservations(rows)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// deletedAtValue devuelve el tiempo de borrado o el cero si la observación está viva, para
// reutilizar nullTime al insertar.
func deletedAtValue(o domain.Observation) time.Time {
	if o.DeletedAt != nil {
		return *o.DeletedAt
	}
	return time.Time{}
}

// normalizeLimit aplica un límite por defecto razonable cuando el solicitado no es positivo.
func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	return limit
}
