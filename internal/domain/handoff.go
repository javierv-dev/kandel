package domain

import "time"

// Handoff is an immutable checkpoint. Its content is a report, not verified evidence.
type Handoff struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	ParentID    string    `json:"parent_id,omitempty"`
	Worktree    string    `json:"-"`
	Title       string    `json:"title"`
	Content     string    `json:"content,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	Application string    `json:"application,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
