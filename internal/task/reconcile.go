package task

import (
	"strings"

	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/tmux"
)

func (m *Manager) Reconcile() error {
	tasks, err := m.tasks.List()
	if err != nil {
		return err
	}

	// Collect live K! windows from both sessions.
	liveWindowIDs := make(map[string]bool)

	for _, session := range []string{m.activeSession, m.parkedSession} {
		windows, _ := tmux.ListWindows(session)
		for _, w := range windows {
			if strings.HasPrefix(w.Name, tmux.WindowPrefix) {
				liveWindowIDs[w.ID] = true
			}
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
			if task.SessionID != "" {
				_ = m.tasks.UpdateState(task.ID, db.StateDormant)
			} else {
				_ = m.tasks.UpdateState(task.ID, db.StateFailed)
			}
			_ = m.tasks.UpdateTmuxWindow(task.ID, "")
		}
	}

	return nil
}
