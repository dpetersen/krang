package task

import (
	"path/filepath"

	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/tmux"
	"github.com/dpetersen/krang/internal/workspace"
)

func (m *Manager) Reconcile() error {
	tasks, err := m.tasks.List()
	if err != nil {
		return err
	}

	m.backfillWorkspaceRepos(tasks)

	// Collect live window IDs from both sessions.
	liveWindowIDs := make(map[string]bool)

	for _, session := range []string{m.activeSession, m.parkedSession} {
		windows, _ := tmux.ListWindows(session)
		for _, w := range windows {
			liveWindowIDs[w.ID] = true
		}
	}

	for _, task := range tasks {
		if task.TmuxWindow == "" {
			continue
		}
		if task.State == db.StateDormant || task.State == db.StateCompleted || task.State == db.StateFailed {
			continue
		}

		// Before marking as gone, double-check the window exists
		// directly. This handles cases where the window is in a
		// session we didn't enumerate.
		if !liveWindowIDs[task.TmuxWindow] && !tmux.WindowExists(task.TmuxWindow) {
			newState := db.StateFailed
			if task.SessionID != "" {
				newState = db.StateDormant
			}
			// Update state before clearing the window. If the
			// state update fails (e.g. SQLITE_BUSY), skip the
			// window clear to avoid leaving the task active with
			// an empty TmuxWindow — that causes Park/Kill to
			// target the current window instead.
			if err := m.tasks.UpdateState(task.ID, newState); err != nil {
				continue
			}
			_ = m.tasks.UpdateTmuxWindow(task.ID, "")
		}
	}

	return nil
}

// backfillWorkspaceRepos records provenance for workspaces that predate
// the workspace_repos table. Their layout still matches the derivation
// cleanup used to perform inline — one working copy per repo-named
// subdirectory, all sharing a VCS identity named after the workspace
// directory — so capture it once instead of re-deriving it forever.
// Anything left unrecorded keeps the filesystem-scan fallback.
func (m *Manager) backfillWorkspaceRepos(tasks []db.Task) {
	if m.workspaceRepos == nil || m.repoSets == nil {
		return
	}

	for _, task := range tasks {
		if task.WorkspaceDir == "" {
			continue
		}

		// Tasks that forked into somebody else's workspace have no VCS
		// identity of their own — the working copies there belong to
		// the task the directory is named after.
		if filepath.Base(task.WorkspaceDir) != task.Name {
			continue
		}

		existing, err := m.workspaceRepos.ListByTask(task.ID)
		if err != nil || len(existing) > 0 {
			continue
		}

		for _, derived := range workspace.DeriveProvenance(m.repoSets, task.WorkspaceDir) {
			row := &db.WorkspaceRepo{
				TaskID:   task.ID,
				RepoName: derived.RepoName,
				DirName:  derived.DirName,
				VCS:      derived.VCS,
				VCSName:  derived.VCSName,
			}
			if err := m.workspaceRepos.Create(row); err != nil {
				_ = m.events.Log(task.ID, "workspace_repo_backfill_failed", err.Error())
			}
		}
	}
}
