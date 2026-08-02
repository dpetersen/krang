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

// openV7DB builds a database at the schema the previous release
// shipped, so migration tests exercise the real upgrade path.
func openV7DB(t *testing.T) *sql.DB {
	t.Helper()

	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Errors are ignored for the same reason migrate ignores them: the
	// ladder re-runs ALTERs whose columns a later schemaV1 already
	// creates for fresh databases.
	for _, stmt := range []string{schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7} {
		_, _ = database.Exec(stmt)
	}
	if _, err := database.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enabling foreign keys: %v", err)
	}
	if _, err := database.Exec("PRAGMA user_version = 7"); err != nil {
		t.Fatalf("setting user_version: %v", err)
	}

	return database
}

func userVersion(t *testing.T, database *sql.DB) int {
	t.Helper()
	var version int
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("reading user_version: %v", err)
	}
	return version
}

func TestMigrateFromV7ReportsVersion8(t *testing.T) {
	database := openV7DB(t)

	if got := userVersion(t, database); got != 7 {
		t.Fatalf("fixture user_version = %d, want 7", got)
	}

	if err := migrate(database); err != nil {
		t.Fatalf("running migrations: %v", err)
	}

	if got := userVersion(t, database); got != 8 {
		t.Errorf("user_version = %d, want 8", got)
	}
}

func TestMigrateFromV7KeepsExistingTasks(t *testing.T) {
	database := openV7DB(t)

	store := NewTaskStore(database)
	existing := &Task{
		ID: "01OLD", Name: "pre-upgrade", State: StateActive,
		Attention: AttentionOK, Cwd: "/tmp", WorkspaceDir: "/tmp/workspaces/pre-upgrade",
	}
	if err := store.Create(existing); err != nil {
		t.Fatalf("creating task on v7 schema: %v", err)
	}

	if err := migrate(database); err != nil {
		t.Fatalf("running migrations: %v", err)
	}

	tasks, err := store.List()
	if err != nil {
		t.Fatalf("listing tasks after migration: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Name != "pre-upgrade" {
		t.Fatalf("tasks after migration = %+v, want the pre-upgrade task", tasks)
	}
	if tasks[0].WorkspaceDir != "/tmp/workspaces/pre-upgrade" {
		t.Errorf("workspace dir = %q, want it preserved", tasks[0].WorkspaceDir)
	}

	// The new table exists and is usable for the migrated task.
	repos := NewWorkspaceRepoStore(database)
	if err := repos.Create(&WorkspaceRepo{
		TaskID: "01OLD", RepoName: "krang", DirName: "krang", VCS: "jj", VCSName: "pre-upgrade",
	}); err != nil {
		t.Fatalf("creating workspace repo after migration: %v", err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	database := openTestDB(t)

	if err := migrate(database); err != nil {
		t.Fatalf("re-running migrations: %v", err)
	}
	if got := userVersion(t, database); got != 8 {
		t.Errorf("user_version = %d, want 8", got)
	}
}
