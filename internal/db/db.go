package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dpetersen/krang/internal/pathutil"
	_ "modernc.org/sqlite"
)

func Open(cwd string) (*sql.DB, error) {
	dbPath := os.Getenv("KRANG_DB")
	if dbPath == "" {
		dbDir := pathutil.DataDir(cwd)
		if err := os.MkdirAll(dbDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating instance dir: %w", err)
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
	var version int
	_ = database.QueryRow("PRAGMA user_version").Scan(&version)

	// Bootstrap: databases created before version tracking have
	// user_version=0 but may already have migrations applied.
	if version == 0 {
		version = detectSchemaVersion(database)
	}

	if version < 1 {
		if _, err := database.Exec(schemaV1); err != nil {
			return err
		}
	}
	if version < 2 {
		// V2: recreate tasks table without UNIQUE on name.
		// Must disable foreign keys around the table swap.
		database.Exec("PRAGMA foreign_keys=OFF")
		database.Exec(schemaV2)
		database.Exec("PRAGMA foreign_keys=ON")
	}
	if version < 3 {
		database.Exec(schemaV3)
	}
	if version < 4 {
		database.Exec(schemaV4)
	}
	if version < 5 {
		database.Exec(schemaV5)
	}

	_, _ = database.Exec("PRAGMA user_version = 5")
	return nil
}

// detectSchemaVersion inspects the existing table structure to
// determine which migrations have already been applied. This handles
// databases created before user_version tracking was added.
func detectSchemaVersion(database *sql.DB) int {
	var name string
	err := database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='tasks'",
	).Scan(&name)
	if err != nil {
		return 0
	}

	if hasColumn(database, "tasks", "flags") {
		return 4
	}
	if hasColumn(database, "tasks", "transcript_path") {
		return 3
	}
	// V2 removed the UNIQUE constraint on name. If we're here the
	// table exists but lacks newer columns — V2 is safe to re-run
	// since there's no data in those columns to lose.
	return 1
}

func hasColumn(database *sql.DB, table, column string) bool {
	rows, err := database.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var colName, colType string
		var dflt *string
		if err := rows.Scan(&cid, &colName, &colType, &notnull, &dflt, &pk); err != nil {
			continue
		}
		if colName == column {
			return true
		}
	}
	return false
}
