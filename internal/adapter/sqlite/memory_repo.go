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
	"github.com/javierv-dev/kandel/internal/project"
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
	unknownRaw, err := encodeUnknownFields(o.UnknownFields)
	if err != nil {
		return 0, fmt.Errorf("sqlite: serializar unknown_fields: %w", err)
	}
	const q = `
		INSERT INTO memories (
			sync_id, session_id, type, title, content, tool_name, project, scope,
			normalized_hash, revision_count, duplicate_count,
			created_at, updated_at, last_seen_at, resolution, conflict_checked, deleted_at, unknown_fields
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := execFromCtx(ctx, r.db).ExecContext(ctx, q,
		nullString(o.SyncID), nullString(o.SessionID), string(o.Type), o.Title, o.Content,
		nullString(o.ToolName), nullString(o.Project), string(o.Scope),
		nullString(o.NormalizedHash),
		o.RevisionCount, o.DuplicateCount,
		formatTime(o.CreatedAt), formatTime(o.UpdatedAt),
		nullTime(o.LastSeenAt), string(o.Resolution), boolToInt(o.ConflictChecked), nullTime(deletedAtValue(o)),
		unknownRaw,
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
	unknownRaw, err := encodeUnknownFields(o.UnknownFields)
	if err != nil {
		return nil, fmt.Errorf("sqlite: serializar unknown_fields: %w", err)
	}
	const q = `
		UPDATE memories SET
			type = ?, title = ?, content = ?, tool_name = ?, project = ?, scope = ?,
			normalized_hash = ?, revision_count = ?, duplicate_count = ?,
			updated_at = ?, last_seen_at = ?, resolution = ?, conflict_checked = ?, unknown_fields = ?
		WHERE id = ? AND deleted_at IS NULL`
	res, err := execFromCtx(ctx, r.db).ExecContext(ctx, q,
		string(o.Type), o.Title, o.Content, nullString(o.ToolName), nullString(o.Project),
		string(o.Scope), nullString(o.NormalizedHash),
		o.RevisionCount, o.DuplicateCount,
		formatTime(o.UpdatedAt), nullTime(o.LastSeenAt), string(o.Resolution), boolToInt(o.ConflictChecked),
		unknownRaw,
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

// supersededClause excluye memorias ocultadas por una relación juzgada: una memoria queda fuera si
// es el target_id de una relación 'supersedes' (su source, más reciente, la reemplaza) o 'duplicate'
// (su source, de mayor calidad, la conserva) ya juzgada. Maneja cadenas A→B→C solo (cada eslabón
// anterior es target). Las memorias con sync_id NULL nunca casan, así que no se excluyen. Se apoya en
// idx_memrel_target(target_id, judgment_status).
const supersededClause = ` AND NOT EXISTS (
		SELECT 1 FROM memory_relations r
		WHERE r.target_id = memories.sync_id
		  AND r.relation IN ('supersedes', 'duplicate')
		  AND r.judgment_status = 'judged')`

// projectMatchSQL evalúa si una columna TEXT con uno o varios proyectos separados por coma
// (p. ej. "yaku-servidor,yaku-web") contiene el proyecto buscado como elemento exacto.
func projectMatchSQL(column string) string {
	return `',' || ifnull(` + column + `, '') || ',' LIKE '%,' || ? || ',%'`
}

// projectScopeClause acota una consulta a un proyecto INCLUYENDO siempre las memorias globales
// (scope = 'global'), que por definición cruzan todos los proyectos, y las memorias compartidas
// entre varios proyectos acoplados que incluyan a project. Un proyecto vacío no filtra (todas).
// Espeja la misma regla que relation_repo.
func projectScopeClause(project string) (string, []any) {
	if project == "" {
		return "", nil
	}
	return ` AND (` + projectMatchSQL("project") + ` OR scope = 'global')`, []any{project}
}

// Recent lista memorias por recencia (camino de navegación del usuario y base de las bandejas de la
// inyección). Por defecto solo las vivas; con q.OnlyDeleted devuelve solo las borradas lógicamente
// (papelera). Aplica los filtros de q; un Limit negativo omite la cláusula LIMIT (sin tope).
func (r *MemoryRepo) Recent(ctx context.Context, q ports.RecentQuery) ([]domain.Memory, error) {
	deletedClause := ` WHERE deleted_at IS NULL`
	if q.OnlyDeleted {
		deletedClause = ` WHERE deleted_at IS NOT NULL`
	}
	query := `SELECT ` + memoryColumns + ` FROM memories` + deletedClause
	args := []any{}
	if clause, a := projectScopeClause(q.Project); clause != "" {
		query += clause
		args = append(args, a...)
	}
	if q.Scope != "" {
		query += ` AND scope = ?`
		args = append(args, string(q.Scope))
	}
	if len(q.Types) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(q.Types)), ",")
		query += ` AND type IN (` + placeholders + `)`
		for _, t := range q.Types {
			args = append(args, string(t))
		}
	}
	if q.ExcludeResolved {
		query += ` AND resolution != 'resolved'`
	}
	if q.Resolution != "" {
		query += ` AND resolution = ?`
		args = append(args, string(q.Resolution))
	}
	if q.ExcludeSuperseded {
		query += supersededClause
	}
	query += ` ORDER BY datetime(created_at) DESC, id DESC`
	if q.Limit >= 0 {
		query += ` LIMIT ?`
		args = append(args, normalizeLimit(q.Limit))
	}
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
	if clause, a := projectScopeClause(q.Project); clause != "" {
		query += clause
		args = append(args, a...)
	}
	if q.Scope != "" {
		query += ` AND scope = ?`
		args = append(args, string(q.Scope))
	}
	if q.ExcludeSessionID != "" {
		query += ` AND ifnull(session_id, '') <> ?`
		args = append(args, q.ExcludeSessionID)
	}
	if !q.IncludeSuperseded {
		query += supersededClause
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
// más antigua a la más nueva (como una cola de TODO: lo más viejo primero). Excluye las
// reemplazadas: un pendiente superseded ya se resolvió por su reemplazo (§20.D/§20.G). Excluye
// también las entradas de bitácora (type = 'journal'): una journal 'open' es el "estado actual" del
// diario, no un pendiente por resolver — vive en la bandeja "Bitácora", no en "Pendientes".
func (r *MemoryRepo) PendingResolution(ctx context.Context, project string, limit int) ([]domain.Memory, error) {
	query := `SELECT ` + memoryColumns + ` FROM memories
		WHERE deleted_at IS NULL
		  AND resolution = 'open'
		  AND type != 'journal'` + supersededClause
	args := []any{}
	if clause, a := projectScopeClause(project); clause != "" {
		query += clause
		args = append(args, a...)
	}
	query += ` ORDER BY datetime(created_at) ASC LIMIT ?`
	args = append(args, normalizeLimit(limit))
	return r.queryMany(ctx, query, args...)
}

// UncheckedNormatives lista las normativas vivas (decision/preference) sobre las que la detección
// automática de conflictos aún no corrió (conflict_checked = 0), de la más antigua a la más nueva.
// Es la cola de trabajo del barrido de detección.
func (r *MemoryRepo) UncheckedNormatives(ctx context.Context, limit int) ([]domain.Memory, error) {
	const q = `SELECT ` + memoryColumns + ` FROM memories
		WHERE deleted_at IS NULL
		  AND conflict_checked = 0
		  AND type IN ('decision', 'preference')
		ORDER BY id ASC LIMIT ?`
	return r.queryMany(ctx, q, normalizeLimit(limit))
}

// UncheckedFactuals lista los factuales vivos (architecture/bugfix/discovery) sobre los que la
// detección de conflictos aún no corrió (conflict_checked = 0), de la más antigua a la más nueva. Es la
// cola de la pasada de supersede factual (§20.E-bis); separada de UncheckedNormatives porque esa pasada
// solo persiste reemplazos (supersedes), no contradicciones para un humano.
func (r *MemoryRepo) UncheckedFactuals(ctx context.Context, limit int) ([]domain.Memory, error) {
	const q = `SELECT ` + memoryColumns + ` FROM memories
		WHERE deleted_at IS NULL
		  AND conflict_checked = 0
		  AND type IN ('architecture', 'bugfix', 'discovery')
		ORDER BY id ASC LIMIT ?`
	return r.queryMany(ctx, q, normalizeLimit(limit))
}

// SetConflictChecked marca una memoria como ya verificada por la detección de conflictos. Es
// idempotente: volver a marcarla no tiene efecto adicional.
func (r *MemoryRepo) SetConflictChecked(ctx context.Context, id int64) error {
	const q = `UPDATE memories SET conflict_checked = 1 WHERE id = ?`
	if _, err := execFromCtx(ctx, r.db).ExecContext(ctx, q, id); err != nil {
		return fmt.Errorf("sqlite: marcar conflict_checked: %w", err)
	}
	return nil
}

// UpdateProject renombra el proyecto de todas las memorias vivas que lo tengan (ya sea como único
// proyecto o dentro de una lista de proyectos acoplados separados por coma); devuelve cuántas filas
// cambió. Lo usa la migración de proyecto (renombrado por cambio de remoto git).
func (r *MemoryRepo) UpdateProject(ctx context.Context, oldProject, newProject string) (int64, error) {
	exec := execFromCtx(ctx, r.db)
	rows, err := exec.QueryContext(ctx,
		`SELECT id, ifnull(project, '') FROM memories WHERE deleted_at IS NULL AND `+projectMatchSQL("project"),
		oldProject,
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: buscar memorias para renombrar proyecto: %w", err)
	}
	type row struct {
		id      int64
		project string
	}
	var matches []row
	for rows.Next() {
		var m row
		if err := rows.Scan(&m.id, &m.project); err != nil {
			rows.Close()
			return 0, fmt.Errorf("sqlite: leer memoria para renombrar proyecto: %w", err)
		}
		matches = append(matches, m)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var updated int64
	for _, m := range matches {
		parts := project.NormalizeList(m.project)
		for i, p := range parts {
			if p == oldProject {
				parts[i] = newProject
			}
		}
		replaced := project.NormalizeProjects(strings.Join(parts, ","))
		res, err := exec.ExecContext(ctx,
			`UPDATE memories SET project = ? WHERE id = ? AND deleted_at IS NULL`,
			replaced, m.id,
		)
		if err != nil {
			return updated, fmt.Errorf("sqlite: renombrar proyecto en memorias: %w", err)
		}
		n, _ := res.RowsAffected()
		updated += n
	}
	return updated, nil
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

// boolToInt convierte un bool al 0/1 que SQLite guarda en columnas INTEGER booleanas.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// normalizeLimit aplica un límite por defecto razonable cuando el solicitado no es positivo.
func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	return limit
}

// Projects devuelve la lista de nombres de proyectos únicos de las memorias vivas, ordenada
// alfabéticamente. Si una memoria pertenece a varios proyectos acoplados ("yaku-servidor,yaku-web"),
// desglosa cada proyecto individualmente.
func (r *MemoryRepo) Projects(ctx context.Context) ([]string, error) {
	const q = `SELECT DISTINCT project FROM memories WHERE deleted_at IS NULL AND ifnull(project, '') != '' ORDER BY project ASC`
	rows, err := execFromCtx(ctx, r.db).QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlite: projects: %w", err)
	}
	defer rows.Close()

	var raw []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		raw = append(raw, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return project.NormalizeList(strings.Join(raw, ",")), nil
}
