package db

import (
	"testing"
)

func TestEventLog(t *testing.T) {
	database := openTestDB(t)
	taskStore := NewTaskStore(database)
	eventStore := NewEventStore(database)

	task := &Task{
		ID: "01ABC", Name: "evented", State: StateActive,
		Attention: AttentionOK, Cwd: "/tmp",
	}
	if err := taskStore.Create(task); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	if err := eventStore.Log("01ABC", "SessionStart", `{"session_id":"xyz"}`); err != nil {
		t.Fatalf("logging event: %v", err)
	}

	if err := eventStore.Log("01ABC", "Stop", `{}`); err != nil {
		t.Fatalf("logging second event: %v", err)
	}

	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM events WHERE task_id = ?", "01ABC").Scan(&count); err != nil {
		t.Fatalf("counting events: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 events, got %d", count)
	}
}

func TestEventLogForeignKeyEnforced(t *testing.T) {
	database := openTestDB(t)
	eventStore := NewEventStore(database)

	err := eventStore.Log("nonexistent", "Stop", `{}`)
	if err == nil {
		t.Error("expected foreign key error, got nil")
	}
}
