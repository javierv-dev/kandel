package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/javierv-dev/kandel/internal/domain"
)

type HandoffRepo struct{ db *sql.DB }

func NewHandoffRepo(db *DB) *HandoffRepo { return &HandoffRepo{db.sql} }

const handoffColumns = `id, task_id, coalesce(parent_id,''), worktree, title, content, session_id, application, created_at`

func (r *HandoffRepo) Add(ctx context.Context, h domain.Handoff) error {
	_, err := execFromCtx(ctx, r.db).ExecContext(ctx, `INSERT INTO handoff_checkpoints(id,task_id,parent_id,worktree,title,content,session_id,application,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, h.ID, h.TaskID, nullString(h.ParentID), h.Worktree, h.Title, h.Content, h.SessionID, h.Application, formatTime(h.CreatedAt))
	return err
}

func (r *HandoffRepo) Get(ctx context.Context, tree, id string) (domain.Handoff, error) {
	return scanHandoff(execFromCtx(ctx, r.db).QueryRowContext(ctx, `SELECT `+handoffColumns+` FROM handoff_checkpoints WHERE worktree=? AND id=?`, tree, id))
}

const handoffHead = `NOT EXISTS(SELECT 1 FROM handoff_checkpoints child WHERE child.parent_id=h.id)`

func (r *HandoffRepo) Latest(ctx context.Context, tree, task string) (domain.Handoff, error) {
	return scanHandoff(execFromCtx(ctx, r.db).QueryRowContext(ctx, `SELECT `+handoffColumns+` FROM handoff_checkpoints h WHERE worktree=? AND task_id=? AND `+handoffHead, tree, task))
}

func (r *HandoffRepo) List(ctx context.Context, tree string, limit int) ([]domain.Handoff, error) {
	rows, err := execFromCtx(ctx, r.db).QueryContext(ctx, `SELECT id,task_id,coalesce(parent_id,''),worktree,title,'',session_id,application,created_at FROM handoff_checkpoints h WHERE worktree=? AND `+handoffHead+` ORDER BY created_at DESC,id LIMIT ?`, tree, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Handoff, 0)
	for rows.Next() {
		h, err := scanHandoff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (r *HandoffRepo) Resume(ctx context.Context, checkpoint, session string) error {
	_, err := execFromCtx(ctx, r.db).ExecContext(ctx, `INSERT INTO handoff_resumptions(checkpoint_id,session_id) VALUES (?,?) ON CONFLICT DO NOTHING`, checkpoint, session)
	return err
}

func scanHandoff(row rowScanner) (domain.Handoff, error) {
	var h domain.Handoff
	var date string
	err := row.Scan(&h.ID, &h.TaskID, &h.ParentID, &h.Worktree, &h.Title, &h.Content, &h.SessionID, &h.Application, &date)
	if errors.Is(err, sql.ErrNoRows) {
		return h, domain.ErrNotFound
	}
	if err != nil {
		return h, err
	}
	h.CreatedAt, err = parseTime(date)
	return h, err
}
