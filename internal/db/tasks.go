package db

import (
	"database/sql"
	"fmt"
	"time"
)

type TaskState string

const (
	StateActive    TaskState = "active"
	StateParked    TaskState = "parked"
	StateDormant   TaskState = "dormant"
	StateCompleted TaskState = "completed"
	StateFailed    TaskState = "failed"
)

type AttentionState string

const (
	AttentionOK         AttentionState = "ok"
	AttentionWaiting    AttentionState = "waiting"
	AttentionPermission AttentionState = "permission"
	AttentionError      AttentionState = "error"
	AttentionDone       AttentionState = "done"
)

type Task struct {
	ID          string
	Name        string
	Prompt      string
	State       TaskState
	Attention   AttentionState
	SessionID   string
	Cwd         string
	TmuxWindow     string
	Summary        string
	SummaryHash    string
	TranscriptPath string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type TaskStore struct {
	db *sql.DB
}

func NewTaskStore(database *sql.DB) *TaskStore {
	return &TaskStore{db: database}
}

func (s *TaskStore) Create(task *Task) error {
	_, err := s.db.Exec(
		`INSERT INTO tasks (id, name, prompt, state, attention, session_id, cwd, tmux_window)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.Name, task.Prompt, task.State, task.Attention,
		task.SessionID, task.Cwd, task.TmuxWindow,
	)
	if err != nil {
		return fmt.Errorf("creating task: %w", err)
	}
	return nil
}

func (s *TaskStore) NameInUse(name string) bool {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE name = ? AND state NOT IN ('completed', 'failed')`,
		name,
	).Scan(&count)
	return err == nil && count > 0
}

func (s *TaskStore) List() ([]Task, error) {
	rows, err := s.db.Query(
		`SELECT id, name, COALESCE(prompt, ''), state, attention,
		        COALESCE(session_id, ''), cwd, COALESCE(tmux_window, ''),
		        summary, summary_hash, transcript_path, created_at, updated_at
		 FROM tasks
		 WHERE state NOT IN ('completed', 'failed')
		 ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()

	return scanTasks(rows)
}

func (s *TaskStore) ListAll() ([]Task, error) {
	rows, err := s.db.Query(
		`SELECT id, name, COALESCE(prompt, ''), state, attention,
		        COALESCE(session_id, ''), cwd, COALESCE(tmux_window, ''),
		        summary, summary_hash, transcript_path, created_at, updated_at
		 FROM tasks
		 ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing all tasks: %w", err)
	}
	defer rows.Close()

	return scanTasks(rows)
}

func (s *TaskStore) GetBySessionID(sessionID string) (*Task, error) {
	row := s.db.QueryRow(
		`SELECT id, name, COALESCE(prompt, ''), state, attention,
		        COALESCE(session_id, ''), cwd, COALESCE(tmux_window, ''),
		        summary, summary_hash, transcript_path, created_at, updated_at
		 FROM tasks WHERE session_id = ?`,
		sessionID,
	)
	return scanTask(row)
}

func (s *TaskStore) UpdateState(id string, state TaskState) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET state = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE id = ?`,
		state, id,
	)
	return err
}

func (s *TaskStore) UpdateAttention(id string, attention AttentionState) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET attention = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE id = ?`,
		attention, id,
	)
	return err
}

func (s *TaskStore) UpdateCwd(id, cwd string) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET cwd = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE id = ?`,
		cwd, id,
	)
	return err
}

func (s *TaskStore) UpdateTranscriptPath(id, transcriptPath string) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET transcript_path = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE id = ?`,
		transcriptPath, id,
	)
	return err
}

func (s *TaskStore) UpdateSessionID(id, sessionID string) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET session_id = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE id = ?`,
		sessionID, id,
	)
	return err
}

func (s *TaskStore) UpdateTmuxWindow(id string, tmuxWindow string) error {
	var windowVal any
	if tmuxWindow == "" {
		windowVal = nil
	} else {
		windowVal = tmuxWindow
	}
	_, err := s.db.Exec(
		`UPDATE tasks SET tmux_window = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE id = ?`,
		windowVal, id,
	)
	return err
}

func (s *TaskStore) UpdateSummary(id, summary, summaryHash string) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET summary = ?, summary_hash = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE id = ?`,
		summary, summaryHash, id,
	)
	return err
}

func (s *TaskStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	return err
}

func scanTasks(rows *sql.Rows) ([]Task, error) {
	var tasks []Task
	for rows.Next() {
		var t Task
		var createdAt, updatedAt string
		if err := rows.Scan(
			&t.ID, &t.Name, &t.Prompt, &t.State, &t.Attention,
			&t.SessionID, &t.Cwd, &t.TmuxWindow,
			&t.Summary, &t.SummaryHash, &t.TranscriptPath, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning task: %w", err)
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		t.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func scanTask(row *sql.Row) (*Task, error) {
	var t Task
	var createdAt, updatedAt string
	if err := row.Scan(
		&t.ID, &t.Name, &t.Prompt, &t.State, &t.Attention,
		&t.SessionID, &t.Cwd, &t.TmuxWindow,
		&t.Summary, &t.SummaryHash, &t.TranscriptPath, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	t.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &t, nil
}
