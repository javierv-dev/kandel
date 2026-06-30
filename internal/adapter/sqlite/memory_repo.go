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

// MemoryRepo implementa ports.MemoryRepo sobre SQLite. Es persistencia pura: no
// decide dedupe (eso vive en service/memory). Aquí solo se guarda y se recupera;
// el método FindByHash le da al servicio lo que necesita para tomar esas decisiones.
type MemoryRepo struct {
	db execer
}

// Aserción en tiempo de compilación: el repo cumple el contrato del port.
var _ ports.MemoryRepo = (*MemoryRepo)(nil)

// NewMemoryRepo crea el repositorio sobre la conexión dada.
func NewMemoryRepo(db *DB) *MemoryRepo {
	return &MemoryRepo{db: db.sql}
}

// Add inserta una memoria nueva con los valores ya resueltos por el servicio (incluidos
// los timestamps). Devuelve el id local autogenerado.
func (r *MemoryRepo) Add(ctx context.Context, o domain.Memory) (int64, error) {
	const q = `
		INSERT INTO memories (
			sync_id, session_id, type, title, content, tool_name, project, scope,
			normalized_hash, revision_count, duplicate_count,
			created_at, updated_at, last_seen_at, resolution, deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := execFromCtx(ctx, r.db).ExecContext(ctx, q,
		nullString(o.SyncID), nullString(o.SessionID), string(o.Type), o.Title, o.Content,
		nullString(o.ToolName), nullString(o.Project), string(o.Scope),
		nullString(o.NormalizedHash),
		o.RevisionCount, o.DuplicateCount,
		formatTime(o.CreatedAt), formatTime(o.UpdatedAt),
		nullTime(o.LastSeenAt), string(o.Resolution), nullTime(deletedAtValue(o)),
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: insertar memoria: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("sqlite: id de memoria: %w", err)
	}
	return id, nil
}

// Get recupera una memoria viva por id; devuelve (nil, nil) si no existe.
func (r *MemoryRepo) Get(ctx context.Context, id int64) (*domain.Memory, error) {
	q := `SELECT ` + memoryColumns + ` FROM memories WHERE id = ? AND deleted_at IS NULL`
	return r.queryOne(ctx, q, id)
}

// GetBySyncID recupera por identidad estable de sync; (nil, nil) si no existe.
func (r *MemoryRepo) GetBySyncID(ctx context.Context, syncID string) (*domain.Memory, error) {
	q := `SELECT ` + memoryColumns + `
		FROM memories WHERE sync_id = ? AND deleted_at IS NULL
		ORDER BY id DESC LIMIT 1`
	return r.queryOne(ctx, q, syncID)
}

// Update reemplaza los campos editables de una memoria y devuelve su estado final. Los
// timestamps (updated_at) los fija el servicio en la entidad recibida.
func (r *MemoryRepo) Update(ctx context.Context, id int64, o domain.Memory) (*domain.Memory, error) {
	const q = `
		UPDATE memories SET
			type = ?, title = ?, content = ?, tool_name = ?, project = ?, scope = ?,
			normalized_hash = ?, revision_count = ?, duplicate_count = ?,
			updated_at = ?, last_seen_at = ?, resolution = ?
		WHERE id = ? AND deleted_at IS NULL`
	res, err := execFromCtx(ctx, r.db).ExecContext(ctx, q,
		string(o.Type), o.Title, o.Content, nullString(o.ToolName), nullString(o.Project),
		string(o.Scope), nullString(o.NormalizedHash),
		o.RevisionCount, o.DuplicateCount,
		formatTime(o.UpdatedAt), nullTime(o.LastSeenAt), string(o.Resolution),
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: actualizar memoria: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return r.Get(ctx, id)
}

// SoftDelete marca la memoria como borrada sin quitarla. La marca de tiempo la pone la base
// de datos (datetime('now')): es un dato de auditoría, no una regla de dominio.
func (r *MemoryRepo) SoftDelete(ctx context.Context, id int64) error {
	const q = `UPDATE memories SET deleted_at = datetime('now') WHERE id = ? AND deleted_at IS NULL`
	if _, err := execFromCtx(ctx, r.db).ExecContext(ctx, q, id); err != nil {
		return fmt.Errorf("sqlite: soft-delete memoria: %w", err)
	}
	return nil
}

// Restore revierte un soft-delete limpiando deleted_at. Solo afecta filas borradas lógicamente;
// si no se afectó ninguna (no existe o no estaba borrada), devuelve domain.ErrNotFound. El vector
// no se toca (el soft-delete nunca lo borró), así que la memoria vuelve a ser buscable al instante.
func (r *MemoryRepo) Restore(ctx context.Context, id int64) error {
	const q = `UPDATE memories SET deleted_at = NULL WHERE id = ? AND deleted_at IS NOT NULL`
	res, err := execFromCtx(ctx, r.db).ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("sqlite: restaurar memoria: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// HardDelete elimina físicamente la memoria.
func (r *MemoryRepo) HardDelete(ctx context.Context, id int64) error {
	if _, err := execFromCtx(ctx, r.db).ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: hard-delete memoria: %w", err)
	}
	return nil
}

// Recent lista memorias vivas por recencia (camino de navegación del usuario).
func (r *MemoryRepo) Recent(ctx context.Context, q ports.RecentQuery) ([]domain.Memory, error) {
	query := `SELECT ` + memoryColumns + ` FROM memories WHERE deleted_at IS NULL`
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
// memorias borradas o que no pasen los filtros se descartan.
func (r *MemoryRepo) Search(ctx context.Context, q ports.SearchQuery, neighbors []ports.Neighbor) ([]domain.SearchResult, error) {
	if len(neighbors) == 0 {
		return nil, nil
	}
	// Mapa id→score para reconstruir orden y puntuación tras leer las filas (que vuelven en
	// orden arbitrario de la base de datos).
	scoreByID := make(map[int64]float64, len(neighbors))
	ids := make([]any, 0, len(neighbors))
	for _, n := range neighbors {
		scoreByID[n.MemoryID] = n.Score
		ids = append(ids, n.MemoryID)
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	query := `SELECT ` + memoryColumns + ` FROM memories
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
	byID := make(map[int64]domain.Memory, len(found))
	for _, o := range found {
		byID[o.ID] = o
	}

	// Recorrer los vecinos en su orden original (de más a menos cercano) y emitir solo los que
	// sobrevivieron a los filtros, adjuntando su puntuación.
	limit := normalizeLimit(q.Limit)
	out := make([]domain.SearchResult, 0, len(neighbors))
	for _, n := range neighbors {
		o, ok := byID[n.MemoryID]
		if !ok {
			continue
		}
		out = append(out, domain.SearchResult{Memory: o, Score: scoreByID[n.MemoryID]})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// FindByHash devuelve la memoria viva más reciente con el mismo hash normalizado dentro del
// mismo proyecto y alcance (base del dedupe exacto). (nil, nil) si no hay ninguna.
func (r *MemoryRepo) FindByHash(ctx context.Context, project string, scope domain.Scope, hash string) (*domain.Memory, error) {
	q := `SELECT ` + memoryColumns + ` FROM memories
		WHERE normalized_hash = ?
		  AND ifnull(project, '') = ?
		  AND scope = ?
		  AND deleted_at IS NULL
		ORDER BY datetime(created_at) DESC, id DESC LIMIT 1`
	return r.queryOne(ctx, q, hash, project, string(scope))
}

// PendingResolution lista memorias vivas pendientes por resolver (resolution = 'open'), de la
// más antigua a la más nueva (como una cola de TODO: lo más viejo primero).
func (r *MemoryRepo) PendingResolution(ctx context.Context, project string, limit int) ([]domain.Memory, error) {
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE deleted_at IS NULL
		  AND resolution = 'open'`
	args := []any{}
	if project != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}
	query += ` ORDER BY datetime(created_at) ASC LIMIT ?`
	args = append(args, normalizeLimit(limit))
	return r.queryMany(ctx, query, args...)
}

// UpdateProject renombra el proyecto de todas las memorias vivas que lo tengan; devuelve cuántas
// filas cambió. Lo usa la migración de proyecto (renombrado por cambio de remoto git).
func (r *MemoryRepo) UpdateProject(ctx context.Context, oldProject, newProject string) (int64, error) {
	res, err := execFromCtx(ctx, r.db).ExecContext(ctx,
		`UPDATE memories SET project = ? WHERE project = ? AND deleted_at IS NULL`, newProject, oldProject)
	if err != nil {
		return 0, fmt.Errorf("sqlite: renombrar proyecto en memorias: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ─── helpers internos ────────────────────────────────────────────────────────

// queryOne ejecuta una consulta que devuelve a lo sumo una fila; (nil, nil) si no hay ninguna.
func (r *MemoryRepo) queryOne(ctx context.Context, query string, args ...any) (*domain.Memory, error) {
	o, err := scanMemory(execFromCtx(ctx, r.db).QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: leer memoria: %w", err)
	}
	return &o, nil
}

// queryMany ejecuta una consulta que devuelve varias filas.
func (r *MemoryRepo) queryMany(ctx context.Context, query string, args ...any) ([]domain.Memory, error) {
	rows, err := execFromCtx(ctx, r.db).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: consultar memorias: %w", err)
	}
	return scanMemories(rows)
}

// deletedAtValue devuelve el tiempo de borrado o el cero si la memoria está viva, para
// reutilizar nullTime al insertar.
func deletedAtValue(o domain.Memory) time.Time {
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
