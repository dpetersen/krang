package tui

import (
	"testing"

	"github.com/dpetersen/krang/internal/db"
)

func tasksNamed(ids ...string) []db.Task {
	tasks := make([]db.Task, 0, len(ids))
	for _, id := range ids {
		tasks = append(tasks, db.Task{ID: id, Name: id, State: db.StateActive})
	}
	return tasks
}

func TestApplyPendingSelectionFocusesNewTask(t *testing.T) {
	m := Model{
		tasks:        tasksNamed("a", "b", "c"),
		cursor:       0,
		selectTaskID: "c",
	}

	m.applyPendingSelection()

	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2", m.cursor)
	}
	if m.selectTaskID != "" {
		t.Errorf("selectTaskID = %q, want cleared", m.selectTaskID)
	}
}

// A refresh issued before the task was created can land after
// taskCreatedMsg. It must not consume the pending selection, or the
// cursor never reaches the new task.
func TestApplyPendingSelectionSurvivesStaleRefresh(t *testing.T) {
	m := Model{
		tasks:        tasksNamed("a", "b"),
		cursor:       0,
		selectTaskID: "c",
	}

	m.applyPendingSelection()

	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (unchanged)", m.cursor)
	}
	if m.selectTaskID != "c" {
		t.Errorf("selectTaskID = %q, want %q to stay pending", m.selectTaskID, "c")
	}

	// The task lands in the next refresh.
	m.tasks = tasksNamed("a", "b", "c")
	m.applyPendingSelection()

	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2 after task appears", m.cursor)
	}
	if m.selectTaskID != "" {
		t.Errorf("selectTaskID = %q, want cleared", m.selectTaskID)
	}
}

// When the task exists but the active filter hides it there is nothing
// to focus, so the request is dropped rather than left armed.
func TestApplyPendingSelectionAbandonsFilteredOutTask(t *testing.T) {
	m := Model{
		tasks:        tasksNamed("alpha", "beta"),
		cursor:       0,
		filterText:   "alpha",
		selectTaskID: "beta",
	}

	m.applyPendingSelection()

	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (unchanged)", m.cursor)
	}
	if m.selectTaskID != "" {
		t.Errorf("selectTaskID = %q, want cleared so it can't fire later", m.selectTaskID)
	}
}

func TestApplyPendingSelectionNoopWhenUnset(t *testing.T) {
	m := Model{tasks: tasksNamed("a", "b"), cursor: 1}

	m.applyPendingSelection()

	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (unchanged)", m.cursor)
	}
}
