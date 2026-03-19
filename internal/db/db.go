package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Open() (*sql.DB, error) {
	dbPath := os.Getenv("KRANG_DB")
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("finding home dir: %w", err)
		}
		dbDir := filepath.Join(home, ".config", "krang")
		if err := os.MkdirAll(dbDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating config dir: %w", err)
		}
		dbPath = filepath.Join(dbDir, "krang.db")
	} else {
		dbDir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dbDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating db dir: %w", err)
		}
	}
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
	if _, err := database.Exec(schemaV1); err != nil {
		return err
	}
	// V2: recreate tasks table without UNIQUE on name, with transcript_path.
	// Must disable foreign keys around the table swap.
	database.Exec("PRAGMA foreign_keys=OFF")
	database.Exec(schemaV2)
	database.Exec("PRAGMA foreign_keys=ON")
	// V3: add transcript_path for DBs where V2 ran before it included the column.
	database.Exec(schemaV3)
	return nil
}
