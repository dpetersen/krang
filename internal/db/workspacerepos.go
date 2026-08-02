package db

import (
	"database/sql"
	"fmt"
	"time"
)

// WorkspaceRepo records one working copy inside a task's workspace
// directory: the repo under the repos dir it was created from and the
// VCS identity (jj workspace name, git branch task name) it was created
// with. Cleanup reads these rows rather than inferring identity from
// directory names, which only works while a task holds at most one
// working copy per repo.
type WorkspaceRepo struct {
	ID           int64
	TaskID       string
	RepoName     string
	DirName      string // relative to the task's workspace dir
	VCS          string // "jj" or "git"
	VCSName      string // jj workspace name / git branch task name
	SlotLabel    string // empty for a task's initial working copy
	BaseRevision string // empty when created from the VCS default
	CreatedAt    time.Time
}

type WorkspaceRepoStore struct {
	db *sql.DB
}

func NewWorkspaceRepoStore(database *sql.DB) *WorkspaceRepoStore {
	return &WorkspaceRepoStore{db: database}
}

// Create inserts a workspace repo row and fills in its generated ID.
func (s *WorkspaceRepoStore) Create(repo *WorkspaceRepo) error {
	result, err := s.db.Exec(
		`INSERT INTO workspace_repos (task_id, repo_name, dir_name, vcs, vcs_name, slot_label, base_revision)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		repo.TaskID, repo.RepoName, repo.DirName, repo.VCS, repo.VCSName, repo.SlotLabel, repo.BaseRevision,
	)
	if err != nil {
		return fmt.Errorf("creating workspace repo: %w", err)
	}
	repo.ID, _ = result.LastInsertId()
	return nil
}

func (s *WorkspaceRepoStore) ListByTask(taskID string) ([]WorkspaceRepo, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, repo_name, dir_name, vcs, vcs_name, slot_label, base_revision, created_at
		 FROM workspace_repos
		 WHERE task_id = ?
		 ORDER BY created_at ASC, id ASC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing workspace repos: %w", err)
	}
	defer rows.Close()

	var repos []WorkspaceRepo
	for rows.Next() {
		var r WorkspaceRepo
		var createdAt string
		if err := rows.Scan(
			&r.ID, &r.TaskID, &r.RepoName, &r.DirName, &r.VCS,
			&r.VCSName, &r.SlotLabel, &r.BaseRevision, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning workspace repo: %w", err)
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

func (s *WorkspaceRepoStore) DeleteByTask(taskID string) error {
	_, err := s.db.Exec(`DELETE FROM workspace_repos WHERE task_id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("deleting workspace repos: %w", err)
	}
	return nil
}

// DeleteByDir removes the row for a single working copy, for when one
// slot is torn down without completing the task.
func (s *WorkspaceRepoStore) DeleteByDir(taskID, dirName string) error {
	_, err := s.db.Exec(
		`DELETE FROM workspace_repos WHERE task_id = ? AND dir_name = ?`,
		taskID, dirName,
	)
	if err != nil {
		return fmt.Errorf("deleting workspace repo: %w", err)
	}
	return nil
}
