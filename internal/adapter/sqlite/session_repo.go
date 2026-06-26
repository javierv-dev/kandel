package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/javierv-dev/kandel/internal/domain"
	"github.com/javierv-dev/kandel/internal/ports"
)

// SessionRepo implementa ports.SessionRepo sobre SQLite.
type SessionRepo struct {
	db execer
}

var _ ports.SessionRepo = (*SessionRepo)(nil)

// NewSessionRepo crea el repositorio de sesiones sobre la conexión dada.
func NewSessionRepo(db *DB) *SessionRepo {
	return &SessionRepo{db: db.sql}
}

// Create abre una sesión nueva con su instante de inicio ya fijado por el servicio.
func (r *SessionRepo) Create(ctx context.Context, s domain.Session) error {
	const q = `INSERT INTO sessions (id, project, directory, started_at, ended_at, summary)
		VALUES (?, ?, ?, ?, ?, ?)`
	_, err := execFromCtx(ctx, r.db).ExecContext(ctx, q,
		s.ID, s.Project, s.Directory, formatTime(s.StartedAt),
		endedAtValue(s), nullString(s.Summary),
	)
	if err != nil {
		return fmt.Errorf("sqlite: crear sesión: %w", err)
	}
	return nil
}

// End cierra una sesión y le adjunta un resumen. El instante de cierre lo pone la base de datos.
func (r *SessionRepo) End(ctx context.Context, id, summary string) error {
	const q = `UPDATE sessions SET ended_at = datetime('now'), summary = ? WHERE id = ?`
	if _, err := execFromCtx(ctx, r.db).ExecContext(ctx, q, nullString(summary), id); err != nil {
		return fmt.Errorf("sqlite: cerrar sesión: %w", err)
	}
	return nil
}

// Get recupera una sesión por id; (nil, nil) si no existe.
func (r *SessionRepo) Get(ctx context.Context, id string) (*domain.Session, error) {
	const q = `SELECT id, project, directory, started_at, ended_at, ifnull(summary, '')
		FROM sessions WHERE id = ?`
	s, err := scanSession(execFromCtx(ctx, r.db).QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: leer sesión: %w", err)
	}
	return &s, nil
}

// Recent lista las sesiones más recientes de un proyecto (por inicio descendente).
func (r *SessionRepo) Recent(ctx context.Context, project string, limit int) ([]domain.Session, error) {
	query := `SELECT id, project, directory, started_at, ended_at, ifnull(summary, '')
		FROM sessions`
	args := []any{}
	if project != "" {
		query += ` WHERE project = ?`
		args = append(args, project)
	}
	query += ` ORDER BY datetime(started_at) DESC, id DESC LIMIT ?`
	args = append(args, normalizeLimit(limit))

	rows, err := execFromCtx(ctx, r.db).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: consultar sesiones: %w", err)
	}
	defer rows.Close()
	var out []domain.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// MostRecentActive devuelve el id de la sesión abierta (sin cerrar) más reciente del proyecto.
func (r *SessionRepo) MostRecentActive(ctx context.Context, project string) (string, bool, error) {
	if project == "" {
		return "", false, nil
	}
	const q = `SELECT id FROM sessions
		WHERE project = ? AND ended_at IS NULL
		ORDER BY datetime(started_at) DESC, id DESC LIMIT 1`
	var id string
	err := execFromCtx(ctx, r.db).QueryRowContext(ctx, q, project).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sqlite: sesión activa: %w", err)
	}
	return id, true, nil
}

// scanSession escanea una fila al modelo de dominio (ended_at anulable → *time.Time).
func scanSession(sc rowScanner) (domain.Session, error) {
	var (
		s       domain.Session
		started string
		ended   sql.NullString
	)
	if err := sc.Scan(&s.ID, &s.Project, &s.Directory, &started, &ended, &s.Summary); err != nil {
		return domain.Session{}, err
	}
	var err error
	if s.StartedAt, err = parseTime(started); err != nil {
		return domain.Session{}, err
	}
	if ended.Valid {
		t, err := parseTime(ended.String)
		if err != nil {
			return domain.Session{}, err
		}
		s.EndedAt = &t
	}
	return s, nil
}

// endedAtValue serializa el cierre de una sesión (NULL si sigue abierta).
func endedAtValue(s domain.Session) any {
	if s.EndedAt == nil {
		return nil
	}
	return formatTime(*s.EndedAt)
}
