package task

import (
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/tmux"
)

func (m *Manager) Reconcile() error {
	tasks, err := m.tasks.List()
	if err != nil {
		return err
	}

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
