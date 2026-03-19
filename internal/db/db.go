package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Open() (*sql.DB, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("finding home dir: %w", err)
	}

	dbDir := filepath.Join(home, ".config", "krang")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating config dir: %w", err)
	}

	dbPath := filepath.Join(dbDir, "krang.db")
	database, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if _, err := database.Exec("PRAGMA journal_mode=WAL"); err != nil {
		database.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}

	if _, err := database.Exec("PRAGMA foreign_keys=ON"); err != nil {
		database.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	if err := migrate(database); err != nil {
		database.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return database, nil
}

func migrate(database *sql.DB) error {
	_, err := database.Exec(schemaV1)
	return err
}
