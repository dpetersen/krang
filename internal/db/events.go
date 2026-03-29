package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type EventStore struct {
	db *sql.DB
}

func NewEventStore(database *sql.DB) *EventStore {
	return &EventStore{db: database}
}

type ActivityEvent struct {
	EventType string
	CreatedAt time.Time
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

// ActivityEvents returns timestamped events for the given tasks since
// the specified time, ordered chronologically. Bucketing is done by the
// caller.
func (s *EventStore) ActivityEvents(taskIDs []string, since time.Time) (map[string][]ActivityEvent, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(taskIDs))
	args := make([]interface{}, 0, len(taskIDs)+1)
	args = append(args, since.UTC().Format("2006-01-02T15:04:05.000Z"))
	for i, id := range taskIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	query := fmt.Sprintf(
		`SELECT task_id, event_type, created_at FROM events
		 WHERE created_at >= ? AND task_id IN (%s)
		 ORDER BY created_at ASC`,
		strings.Join(placeholders, ","),
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying activity events: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]ActivityEvent)
	for rows.Next() {
		var taskID, eventType, createdAtStr string
		if err := rows.Scan(&taskID, &eventType, &createdAtStr); err != nil {
			return nil, fmt.Errorf("scanning activity event: %w", err)
		}
		createdAt, err := time.Parse("2006-01-02T15:04:05.000Z", createdAtStr)
		if err != nil {
			continue
		}
		result[taskID] = append(result[taskID], ActivityEvent{
			EventType: eventType,
			CreatedAt: createdAt,
		})
	}
	return result, rows.Err()
}

// TrimOlderThan deletes events older than the given duration.
func (s *EventStore) TrimOlderThan(d time.Duration) error {
	cutoff := time.Now().UTC().Add(-d).Format("2006-01-02T15:04:05.000Z")
	_, err := s.db.Exec(`DELETE FROM events WHERE created_at < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("trimming old events: %w", err)
	}
	return nil
}
