package db

import (
	"database/sql"
	"fmt"
)

type EventStore struct {
	db *sql.DB
}

func NewEventStore(database *sql.DB) *EventStore {
	return &EventStore{db: database}
}

func (s *EventStore) Log(taskID, eventType, payload string) error {
	_, err := s.db.Exec(
		`INSERT INTO events (task_id, event_type, payload) VALUES (?, ?, ?)`,
		taskID, eventType, payload,
	)
	if err != nil {
		return fmt.Errorf("logging event: %w", err)
	}
	return nil
}
