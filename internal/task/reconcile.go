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

	activeWindows, err := tmux.ListWindows(m.activeSession)
	if err != nil {
		return err
	}
	parkedWindows, err := tmux.ListWindows(tmux.ParkedSession)
	if err != nil {
		return err
	}

	liveWindowIDs := make(map[string]bool)
	for _, w := range activeWindows {
		if strings.HasPrefix(w.Name, tmux.WindowPrefix) {
			liveWindowIDs[w.ID] = true
		}
	}
	for _, w := range parkedWindows {
		if strings.HasPrefix(w.Name, tmux.WindowPrefix) {
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

		if !liveWindowIDs[task.TmuxWindow] {
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
