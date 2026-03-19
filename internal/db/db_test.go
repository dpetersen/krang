package db

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if _, err := database.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enabling foreign keys: %v", err)
	}

	if err := migrate(database); err != nil {
		t.Fatalf("running migrations: %v", err)
	}

	return database
}
