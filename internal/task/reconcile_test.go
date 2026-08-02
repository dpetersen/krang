package task

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/workspace"
)

// backfillFixture builds a metarepo on disk plus a temp database, and
// returns a manager wired the way krang wires one at startup.
type backfillFixture struct {
	manager        *Manager
	tasks          *db.TaskStore
	workspaceRepos *db.WorkspaceRepoStore
	reposDir       string
	workspacesDir  string
}

func newBackfillFixture(t *testing.T) *backfillFixture {
	t.Helper()

	metarepoDir := t.TempDir()
	t.Setenv("KRANG_DB", filepath.Join(metarepoDir, "krang-test.db"))

	database, err := db.Open(metarepoDir)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	reposDir := filepath.Join(metarepoDir, "repos")
	workspacesDir := filepath.Join(metarepoDir, "workspaces")
	repoSets := &workspace.RepoSets{
		MetarepoDir:       metarepoDir,
		WorkspaceStrategy: workspace.StrategyMultiRepo,
		ReposDir:          reposDir,
		WorkspacesDir:     workspacesDir,
		Sets:              map[string][]string{},
	}

	tasks := db.NewTaskStore(database)
	workspaceRepos := db.NewWorkspaceRepoStore(database)
	manager := NewManager(
		tasks, db.NewEventStore(database), workspaceRepos,
		// Sessions that don't exist — reconcile tolerates that, and
		// none of these tasks have a tmux window anyway.
		"krang-backfill-test", "krang-backfill-test-parked",
		nil, "", "", metarepoDir, repoSets,
	)

	return &backfillFixture{
		manager:        manager,
		tasks:          tasks,
		workspaceRepos: workspaceRepos,
		reposDir:       reposDir,
		workspacesDir:  workspacesDir,
	}
}

// makeRepoDir creates a directory carrying the marker that makes krang
// treat it as a checkout of the given VCS.
func makeRepoDir(t *testing.T, dir, vcs string) {
	t.Helper()
	marker := ".git"
	if vcs == "jj" {
		marker = ".jj"
	}
	if err := os.MkdirAll(filepath.Join(dir, marker), 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
}

func TestReconcileBackfillsWorkspaceRepos(t *testing.T) {
	f := newBackfillFixture(t)

	makeRepoDir(t, filepath.Join(f.reposDir, "alpha"), "jj")
	makeRepoDir(t, filepath.Join(f.reposDir, "beta"), "git")

	workspaceDir := filepath.Join(f.workspacesDir, "legacy-task")
	makeRepoDir(t, filepath.Join(workspaceDir, "alpha"), "jj")
	makeRepoDir(t, filepath.Join(workspaceDir, "beta"), "git")
	if err := os.MkdirAll(filepath.Join(workspaceDir, "notes"), 0o755); err != nil {
		t.Fatalf("creating non-repo dir: %v", err)
	}

	if err := f.tasks.Create(&db.Task{
		ID: "01OLD", Name: "legacy-task", State: db.StateActive,
		Attention: db.AttentionOK, Cwd: workspaceDir, WorkspaceDir: workspaceDir,
	}); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	if err := f.manager.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	rows, err := f.workspaceRepos.ListByTask("01OLD")
	if err != nil {
		t.Fatalf("listing workspace repos: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}

	byDir := make(map[string]db.WorkspaceRepo, len(rows))
	for _, row := range rows {
		byDir[row.DirName] = row
	}

	alpha, ok := byDir["alpha"]
	if !ok {
		t.Fatalf("no row for alpha: %+v", rows)
	}
	if alpha.RepoName != "alpha" || alpha.VCS != "jj" || alpha.VCSName != "legacy-task" {
		t.Errorf("alpha row = %+v, want repo alpha / jj / vcs name legacy-task", alpha)
	}
	if alpha.SlotLabel != "" {
		t.Errorf("alpha slot label = %q, want empty for an initial working copy", alpha.SlotLabel)
	}

	beta, ok := byDir["beta"]
	if !ok {
		t.Fatalf("no row for beta: %+v", rows)
	}
	if beta.RepoName != "beta" || beta.VCS != "git" || beta.VCSName != "legacy-task" {
		t.Errorf("beta row = %+v, want repo beta / git / vcs name legacy-task", beta)
	}
}

func TestReconcileBackfillIsIdempotent(t *testing.T) {
	f := newBackfillFixture(t)

	makeRepoDir(t, filepath.Join(f.reposDir, "alpha"), "jj")
	makeRepoDir(t, filepath.Join(f.reposDir, "beta"), "git")

	workspaceDir := filepath.Join(f.workspacesDir, "legacy-task")
	makeRepoDir(t, filepath.Join(workspaceDir, "alpha"), "jj")
	makeRepoDir(t, filepath.Join(workspaceDir, "beta"), "git")

	if err := f.tasks.Create(&db.Task{
		ID: "01OLD", Name: "legacy-task", State: db.StateActive,
		Attention: db.AttentionOK, Cwd: workspaceDir, WorkspaceDir: workspaceDir,
	}); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := f.manager.Reconcile(); err != nil {
			t.Fatalf("Reconcile %d: %v", i, err)
		}
	}

	rows, err := f.workspaceRepos.ListByTask("01OLD")
	if err != nil {
		t.Fatalf("listing workspace repos: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows after repeated reconciles, want 2: %+v", len(rows), rows)
	}
}

func TestReconcileSkipsTasksWithoutWorkspaces(t *testing.T) {
	f := newBackfillFixture(t)

	if err := f.tasks.Create(&db.Task{
		ID: "01CWD", Name: "no-workspace", State: db.StateActive,
		Attention: db.AttentionOK, Cwd: "/tmp",
	}); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	if err := f.manager.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	rows, err := f.workspaceRepos.ListByTask("01CWD")
	if err != nil {
		t.Fatalf("listing workspace repos: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows for a task with no workspace, want 0", len(rows))
	}
}

// A forked task sharing another task's workspace has no VCS identity of
// its own, so backfill leaves it alone rather than recording working
// copies that belong to the workspace's owner.
func TestReconcileSkipsSharedWorkspaceForks(t *testing.T) {
	f := newBackfillFixture(t)

	makeRepoDir(t, filepath.Join(f.reposDir, "alpha"), "jj")

	workspaceDir := filepath.Join(f.workspacesDir, "owner")
	makeRepoDir(t, filepath.Join(workspaceDir, "alpha"), "jj")

	for _, task := range []*db.Task{
		{ID: "01OWNER", Name: "owner", State: db.StateActive, Attention: db.AttentionOK,
			Cwd: workspaceDir, WorkspaceDir: workspaceDir},
		{ID: "01FORK", Name: "fork", State: db.StateActive, Attention: db.AttentionOK,
			Cwd: workspaceDir, WorkspaceDir: workspaceDir, SourceTaskID: "01OWNER"},
	} {
		if err := f.tasks.Create(task); err != nil {
			t.Fatalf("creating task %s: %v", task.Name, err)
		}
	}

	if err := f.manager.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	owner, err := f.workspaceRepos.ListByTask("01OWNER")
	if err != nil {
		t.Fatalf("listing owner rows: %v", err)
	}
	if len(owner) != 1 {
		t.Errorf("owner got %d rows, want 1: %+v", len(owner), owner)
	}

	fork, err := f.workspaceRepos.ListByTask("01FORK")
	if err != nil {
		t.Fatalf("listing fork rows: %v", err)
	}
	if len(fork) != 0 {
		t.Errorf("fork got %d rows, want 0: %+v", len(fork), fork)
	}
}
