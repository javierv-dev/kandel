package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// PromptRepo implementa ports.PromptRepo sobre SQLite.
type PromptRepo struct {
	db execer
}

var _ ports.PromptRepo = (*PromptRepo)(nil)

// NewPromptRepo crea el repositorio de prompts sobre la conexión dada.
func NewPromptRepo(db *DB) *PromptRepo {
	return &PromptRepo{db: db.sql}
}

// Add guarda un prompt y devuelve su id local.
func (r *PromptRepo) Add(ctx context.Context, p domain.Prompt) (int64, error) {
	const q = `INSERT INTO prompts (sync_id, session_id, content, project, created_at)
		VALUES (?, ?, ?, ?, ?)`
	res, err := execFromCtx(ctx, r.db).ExecContext(ctx, q,
		nullString(p.SyncID), nullString(p.SessionID), p.Content, nullString(p.Project), formatTime(p.CreatedAt),
	)
	if err != nil {
		if esViolaciónDeClaveForánea(err) {
			// La única clave foránea de prompts es session_id → sessions.id, así que una violación
			// aquí solo puede significar que la sesión no existe. Se traduce a un error de dominio
			// para que el servicio decida qué hacer sin conocer los códigos de SQLite.
			return 0, fmt.Errorf("sqlite: insertar prompt: %w", domain.ErrUnknownSession)
		}
		return 0, fmt.Errorf("sqlite: insertar prompt: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("sqlite: id de prompt: %w", err)
	}
	return id, nil
}

// AddIfMissing guarda el prompt solo si no existe uno idéntico (misma sesión, proyecto y
// contenido). El bool indica si se insertó (true) o ya existía (false).
func (r *PromptRepo) AddIfMissing(ctx context.Context, p domain.Prompt) (int64, bool, error) {
	const find = `SELECT id FROM prompts
		WHERE ifnull(session_id, '') = ? AND ifnull(project, '') = ? AND content = ?
		ORDER BY id DESC LIMIT 1`
	var existing int64
	err := execFromCtx(ctx, r.db).QueryRowContext(ctx, find, p.SessionID, p.Project, p.Content).Scan(&existing)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("sqlite: buscar prompt: %w", err)
	}
	id, err := r.Add(ctx, p)
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// Recent lista los prompts más recientes de un proyecto.
func (r *PromptRepo) Recent(ctx context.Context, project string, limit int) ([]domain.Prompt, error) {
	query := `SELECT id, ifnull(sync_id, ''), ifnull(session_id, ''), ifnull(project, ''), content, created_at
		FROM prompts`
	args := []any{}
	if project != "" {
		query += ` WHERE project = ?`
		args = append(args, project)
	}
	query += ` ORDER BY datetime(created_at) DESC, id DESC LIMIT ?`
	args = append(args, normalizeLimit(limit))

	rows, err := execFromCtx(ctx, r.db).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: consultar prompts: %w", err)
	}
	defer rows.Close()
	var out []domain.Prompt
	for rows.Next() {
		var (
			p       domain.Prompt
			created string
		)
		if err := rows.Scan(&p.ID, &p.SyncID, &p.SessionID, &p.Project, &p.Content, &created); err != nil {
			return nil, err
		}
		if p.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Delete elimina un prompt por id.
func (r *PromptRepo) Delete(ctx context.Context, id int64) error {
	if _, err := execFromCtx(ctx, r.db).ExecContext(ctx, `DELETE FROM prompts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: borrar prompt: %w", err)
	}
	return nil
}

// UpdateProject renombra el proyecto de todos los prompts que lo tengan; devuelve cuántas filas
// cambió.
func (r *PromptRepo) UpdateProject(ctx context.Context, oldProject, newProject string) (int64, error) {
	res, err := execFromCtx(ctx, r.db).ExecContext(ctx,
		`UPDATE prompts SET project = ? WHERE project = ?`, newProject, oldProject)
	if err != nil {
		return 0, fmt.Errorf("sqlite: renombrar proyecto en prompts: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
