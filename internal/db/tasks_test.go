package db

import (
	"testing"
)

func TestTaskCreate(t *testing.T) {
	store := NewTaskStore(openTestDB(t))

	task := &Task{
		ID:        "01ABC",
		Name:      "test-task",
		Prompt:    "do something",
		State:     StateActive,
		Attention: AttentionOK,
		SessionID: "sess-123",
		Cwd:       "/tmp",
	}

	if err := store.Create(task); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	tasks, err := store.List()
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}

	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Name != "test-task" {
		t.Errorf("expected name test-task, got %s", tasks[0].Name)
	}
	if tasks[0].State != StateActive {
		t.Errorf("expected state active, got %s", tasks[0].State)
	}
}

func TestTaskCreateDuplicateNameFails(t *testing.T) {
	store := NewTaskStore(openTestDB(t))

	task := &Task{
		ID: "01ABC", Name: "dup", State: StateActive, Attention: AttentionOK, Cwd: "/tmp",
	}
	if err := store.Create(task); err != nil {
		t.Fatalf("creating first task: %v", err)
	}

	task2 := &Task{
		ID: "01DEF", Name: "dup", State: StateActive, Attention: AttentionOK, Cwd: "/tmp",
	}
	if err := store.Create(task2); err == nil {
		t.Fatal("expected error creating duplicate name, got nil")
	}
}

func TestTaskStateTransitions(t *testing.T) {
	store := NewTaskStore(openTestDB(t))

	task := &Task{
		ID: "01ABC", Name: "transition", State: StateActive, Attention: AttentionOK, Cwd: "/tmp",
	}
	if err := store.Create(task); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	if err := store.UpdateState("01ABC", StateParked); err != nil {
		t.Fatalf("updating state: %v", err)
	}

	tasks, err := store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if tasks[0].State != StateParked {
		t.Errorf("expected parked, got %s", tasks[0].State)
	}
}

func TestTaskListExcludesCompletedAndFailed(t *testing.T) {
	store := NewTaskStore(openTestDB(t))

	for i, state := range []TaskState{StateActive, StateParked, StateDormant, StateCompleted, StateFailed} {
		task := &Task{
			ID: string(rune('A' + i)), Name: "task-" + string(state),
			State: state, Attention: AttentionOK, Cwd: "/tmp",
		}
		if err := store.Create(task); err != nil {
			t.Fatalf("creating %s task: %v", state, err)
		}
	}

	tasks, err := store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(tasks) != 3 {
		t.Errorf("expected 3 visible tasks, got %d", len(tasks))
	}

	allTasks, err := store.ListAll()
	if err != nil {
		t.Fatalf("listing all: %v", err)
	}
	if len(allTasks) != 5 {
		t.Errorf("expected 5 total tasks, got %d", len(allTasks))
	}
}

func TestTaskGetBySessionID(t *testing.T) {
	store := NewTaskStore(openTestDB(t))

	task := &Task{
		ID: "01ABC", Name: "find-me", State: StateActive,
		Attention: AttentionOK, SessionID: "sess-xyz", Cwd: "/tmp",
	}
	if err := store.Create(task); err != nil {
		t.Fatalf("creating: %v", err)
	}

	found, err := store.GetBySessionID("sess-xyz")
	if err != nil {
		t.Fatalf("getting by session ID: %v", err)
	}
	if found.Name != "find-me" {
		t.Errorf("expected find-me, got %s", found.Name)
	}

	_, err = store.GetBySessionID("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session ID")
	}
}

func TestTaskUpdateAttention(t *testing.T) {
	store := NewTaskStore(openTestDB(t))

	task := &Task{
		ID: "01ABC", Name: "attn", State: StateActive,
		Attention: AttentionOK, Cwd: "/tmp",
	}
	if err := store.Create(task); err != nil {
		t.Fatalf("creating: %v", err)
	}

	if err := store.UpdateAttention("01ABC", AttentionPermission); err != nil {
		t.Fatalf("updating attention: %v", err)
	}

	found, err := store.GetBySessionID("")
	// Can't find by empty session ID, use List instead.
	_ = found
	_ = err

	tasks, err := store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if tasks[0].Attention != AttentionPermission {
		t.Errorf("expected permission, got %s", tasks[0].Attention)
	}
}

func TestTaskUpdateTmuxWindow(t *testing.T) {
	store := NewTaskStore(openTestDB(t))

	task := &Task{
		ID: "01ABC", Name: "win", State: StateActive,
		Attention: AttentionOK, Cwd: "/tmp", TmuxWindow: "@5",
	}
	if err := store.Create(task); err != nil {
		t.Fatalf("creating: %v", err)
	}

	if err := store.UpdateTmuxWindow("01ABC", ""); err != nil {
		t.Fatalf("clearing window: %v", err)
	}

	tasks, err := store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if tasks[0].TmuxWindow != "" {
		t.Errorf("expected empty window, got %s", tasks[0].TmuxWindow)
	}

	if err := store.UpdateTmuxWindow("01ABC", "@10"); err != nil {
		t.Fatalf("setting window: %v", err)
	}

	tasks, err = store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if tasks[0].TmuxWindow != "@10" {
		t.Errorf("expected @10, got %s", tasks[0].TmuxWindow)
	}
}

func TestTaskUpdateSummary(t *testing.T) {
	store := NewTaskStore(openTestDB(t))

	task := &Task{
		ID: "01ABC", Name: "summ", State: StateActive,
		Attention: AttentionOK, Cwd: "/tmp",
	}
	if err := store.Create(task); err != nil {
		t.Fatalf("creating: %v", err)
	}

	if err := store.UpdateSummary("01ABC", "Refactoring auth", "abc123"); err != nil {
		t.Fatalf("updating summary: %v", err)
	}

	tasks, err := store.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if tasks[0].Summary != "Refactoring auth" {
		t.Errorf("expected summary, got %s", tasks[0].Summary)
	}
	if tasks[0].SummaryHash != "abc123" {
		t.Errorf("expected hash abc123, got %s", tasks[0].SummaryHash)
	}
}

func TestTaskDelete(t *testing.T) {
	store := NewTaskStore(openTestDB(t))

	task := &Task{
		ID: "01ABC", Name: "delete-me", State: StateActive,
		Attention: AttentionOK, Cwd: "/tmp",
	}
	if err := store.Create(task); err != nil {
		t.Fatalf("creating: %v", err)
	}

	if err := store.Delete("01ABC"); err != nil {
		t.Fatalf("deleting: %v", err)
	}

	tasks, err := store.ListAll()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks after delete, got %d", len(tasks))
	}
}
