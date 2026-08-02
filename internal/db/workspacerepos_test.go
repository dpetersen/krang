package db

import (
	"database/sql"
	"testing"
)

func seedTask(t *testing.T, database *sql.DB, id, name string) {
	t.Helper()
	store := NewTaskStore(database)
	if err := store.Create(&Task{
		ID: id, Name: name, State: StateActive, Attention: AttentionOK, Cwd: "/tmp",
	}); err != nil {
		t.Fatalf("creating task %s: %v", name, err)
	}
}

func TestWorkspaceRepoCreateAndListByTask(t *testing.T) {
	database := openTestDB(t)
	seedTask(t, database, "01ABC", "slots")
	store := NewWorkspaceRepoStore(database)

	initial := &WorkspaceRepo{
		TaskID: "01ABC", RepoName: "krang", DirName: "krang",
		VCS: "jj", VCSName: "slots",
	}
	if err := store.Create(initial); err != nil {
		t.Fatalf("creating initial repo: %v", err)
	}
	if initial.ID == 0 {
		t.Error("expected Create to fill in the generated ID")
	}

	if err := store.Create(&WorkspaceRepo{
		TaskID: "01ABC", RepoName: "krang", DirName: "krang--tests",
		VCS: "jj", VCSName: "slots--krang--tests", SlotLabel: "tests",
		BaseRevision: "main@origin",
	}); err != nil {
		t.Fatalf("creating slot repo: %v", err)
	}

	repos, err := store.ListByTask("01ABC")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(repos))
	}

	byDir := make(map[string]WorkspaceRepo, len(repos))
	for _, r := range repos {
		byDir[r.DirName] = r
	}

	if got := byDir["krang"]; got.SlotLabel != "" || got.VCSName != "slots" {
		t.Errorf("initial row = %+v, want empty slot label and vcs name slots", got)
	}
	slot := byDir["krang--tests"]
	if slot.RepoName != "krang" {
		t.Errorf("slot repo name = %q, want krang", slot.RepoName)
	}
	if slot.VCSName != "slots--krang--tests" {
		t.Errorf("slot vcs name = %q, want slots--krang--tests", slot.VCSName)
	}
	if slot.SlotLabel != "tests" {
		t.Errorf("slot label = %q, want tests", slot.SlotLabel)
	}
	if slot.BaseRevision != "main@origin" {
		t.Errorf("slot base revision = %q, want main@origin", slot.BaseRevision)
	}
	if slot.CreatedAt.IsZero() {
		t.Error("expected created_at to be populated")
	}
}

func TestWorkspaceRepoListByTaskIsolatesTasks(t *testing.T) {
	database := openTestDB(t)
	seedTask(t, database, "01ABC", "alpha")
	seedTask(t, database, "01DEF", "beta")
	store := NewWorkspaceRepoStore(database)

	if err := store.Create(&WorkspaceRepo{
		TaskID: "01ABC", RepoName: "krang", DirName: "krang", VCS: "jj", VCSName: "alpha",
	}); err != nil {
		t.Fatalf("creating alpha repo: %v", err)
	}
	if err := store.Create(&WorkspaceRepo{
		TaskID: "01DEF", RepoName: "krang", DirName: "krang", VCS: "jj", VCSName: "beta",
	}); err != nil {
		t.Fatalf("creating beta repo: %v", err)
	}

	repos, err := store.ListByTask("01DEF")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(repos) != 1 || repos[0].VCSName != "beta" {
		t.Fatalf("ListByTask(01DEF) = %+v, want only beta's row", repos)
	}
}

func TestWorkspaceRepoDeleteByTask(t *testing.T) {
	database := openTestDB(t)
	seedTask(t, database, "01ABC", "alpha")
	seedTask(t, database, "01DEF", "beta")
	store := NewWorkspaceRepoStore(database)

	for _, repo := range []*WorkspaceRepo{
		{TaskID: "01ABC", RepoName: "krang", DirName: "krang", VCS: "jj", VCSName: "alpha"},
		{TaskID: "01ABC", RepoName: "krang", DirName: "krang--tests", VCS: "jj", VCSName: "alpha--krang--tests"},
		{TaskID: "01DEF", RepoName: "krang", DirName: "krang", VCS: "jj", VCSName: "beta"},
	} {
		if err := store.Create(repo); err != nil {
			t.Fatalf("creating %s: %v", repo.DirName, err)
		}
	}

	if err := store.DeleteByTask("01ABC"); err != nil {
		t.Fatalf("deleting by task: %v", err)
	}

	repos, err := store.ListByTask("01ABC")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("expected no rows for 01ABC, got %d", len(repos))
	}

	survivors, err := store.ListByTask("01DEF")
	if err != nil {
		t.Fatalf("listing survivors: %v", err)
	}
	if len(survivors) != 1 {
		t.Errorf("expected beta's row to survive, got %d rows", len(survivors))
	}
}

func TestWorkspaceRepoDeleteByDir(t *testing.T) {
	database := openTestDB(t)
	seedTask(t, database, "01ABC", "alpha")
	store := NewWorkspaceRepoStore(database)

	for _, repo := range []*WorkspaceRepo{
		{TaskID: "01ABC", RepoName: "krang", DirName: "krang", VCS: "jj", VCSName: "alpha"},
		{TaskID: "01ABC", RepoName: "krang", DirName: "krang--tests", VCS: "jj", VCSName: "alpha--krang--tests"},
	} {
		if err := store.Create(repo); err != nil {
			t.Fatalf("creating %s: %v", repo.DirName, err)
		}
	}

	if err := store.DeleteByDir("01ABC", "krang--tests"); err != nil {
		t.Fatalf("deleting by dir: %v", err)
	}

	repos, err := store.ListByTask("01ABC")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(repos) != 1 || repos[0].DirName != "krang" {
		t.Fatalf("remaining rows = %+v, want only the initial working copy", repos)
	}
}

func TestWorkspaceRepoRejectsDuplicateDir(t *testing.T) {
	database := openTestDB(t)
	seedTask(t, database, "01ABC", "alpha")
	store := NewWorkspaceRepoStore(database)

	if err := store.Create(&WorkspaceRepo{
		TaskID: "01ABC", RepoName: "krang", DirName: "krang", VCS: "jj", VCSName: "alpha",
	}); err != nil {
		t.Fatalf("creating first row: %v", err)
	}

	err := store.Create(&WorkspaceRepo{
		TaskID: "01ABC", RepoName: "other", DirName: "krang", VCS: "git", VCSName: "alpha-other",
	})
	if err == nil {
		t.Error("expected a second row for the same (task, dir) to be rejected")
	}
}

func TestWorkspaceRepoRejectsDuplicateVCSIdentity(t *testing.T) {
	database := openTestDB(t)
	seedTask(t, database, "01ABC", "alpha")
	seedTask(t, database, "01DEF", "beta")
	store := NewWorkspaceRepoStore(database)

	if err := store.Create(&WorkspaceRepo{
		TaskID: "01ABC", RepoName: "krang", DirName: "krang", VCS: "jj", VCSName: "shared",
	}); err != nil {
		t.Fatalf("creating first row: %v", err)
	}

	err := store.Create(&WorkspaceRepo{
		TaskID: "01DEF", RepoName: "krang", DirName: "krang", VCS: "jj", VCSName: "shared",
	})
	if err == nil {
		t.Error("expected two working copies to be rejected for the same (repo, vcs name)")
	}
}

func TestWorkspaceRepoRejectsUnknownVCS(t *testing.T) {
	database := openTestDB(t)
	seedTask(t, database, "01ABC", "alpha")
	store := NewWorkspaceRepoStore(database)

	err := store.Create(&WorkspaceRepo{
		TaskID: "01ABC", RepoName: "krang", DirName: "krang", VCS: "hg", VCSName: "alpha",
	})
	if err == nil {
		t.Error("expected an unknown VCS to be rejected")
	}
}

func TestReassignTaskMovesEveryRow(t *testing.T) {
	database := openTestDB(t)
	seedTask(t, database, "01ABC", "owner")
	seedTask(t, database, "01DEF", "sharer")
	store := NewWorkspaceRepoStore(database)

	for _, row := range []WorkspaceRepo{
		{TaskID: "01ABC", RepoName: "krang", DirName: "krang", VCS: "jj", VCSName: "owner"},
		{TaskID: "01ABC", RepoName: "krang", DirName: "krang--tests", VCS: "jj",
			VCSName: "owner--krang--tests", SlotLabel: "tests"},
	} {
		if err := store.Create(&row); err != nil {
			t.Fatalf("creating %s: %v", row.DirName, err)
		}
	}

	if err := store.ReassignTask("01ABC", "01DEF"); err != nil {
		t.Fatalf("ReassignTask: %v", err)
	}

	if rows, err := store.ListByTask("01ABC"); err != nil || len(rows) != 0 {
		t.Errorf("source still holds %+v (%v)", rows, err)
	}
	rows, err := store.ListByTask("01DEF")
	if err != nil {
		t.Fatalf("listing the target: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("target holds %d rows, want 2: %+v", len(rows), rows)
	}
	// The identities travel intact — that is the whole point of moving
	// the rows rather than re-deriving them.
	if rows[0].VCSName != "owner" || rows[1].VCSName != "owner--krang--tests" {
		t.Errorf("rows = %+v, want the original identities", rows)
	}
}

// A row the target already has an equivalent of would violate
// UNIQUE(task_id, dir_name). The target's own record wins and the
// duplicate is dropped, rather than the whole reassignment failing and
// stranding every row on a completed task.
func TestReassignTaskDropsRowsTheTargetAlreadyHas(t *testing.T) {
	database := openTestDB(t)
	seedTask(t, database, "01ABC", "owner")
	seedTask(t, database, "01DEF", "sharer")
	store := NewWorkspaceRepoStore(database)

	if err := store.Create(&WorkspaceRepo{
		TaskID: "01ABC", RepoName: "krang", DirName: "krang", VCS: "jj", VCSName: "owner",
	}); err != nil {
		t.Fatalf("creating the owner's row: %v", err)
	}
	if err := store.Create(&WorkspaceRepo{
		TaskID: "01DEF", RepoName: "krang", DirName: "krang", VCS: "jj", VCSName: "sharer",
	}); err != nil {
		t.Fatalf("creating the sharer's row: %v", err)
	}

	if err := store.ReassignTask("01ABC", "01DEF"); err != nil {
		t.Fatalf("ReassignTask: %v", err)
	}

	if rows, err := store.ListByTask("01ABC"); err != nil || len(rows) != 0 {
		t.Errorf("source still holds %+v (%v)", rows, err)
	}
	rows, err := store.ListByTask("01DEF")
	if err != nil {
		t.Fatalf("listing the target: %v", err)
	}
	if len(rows) != 1 || rows[0].VCSName != "sharer" {
		t.Errorf("target rows = %+v, want only its own record", rows)
	}
}
