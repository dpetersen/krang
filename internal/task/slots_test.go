package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dpetersen/krang/internal/db"
)

// initGitRepo makes a real repository so slot creation can run the git
// commands it builds instead of only being asserted about.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
}

// slotTask creates a task row with a workspace directory ready for
// working copies, and returns the directory.
func slotTask(t *testing.T, f *managerFixture, taskID, name string) string {
	t.Helper()
	workspaceDir := filepath.Join(f.workspacesDir, name)
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	if err := f.tasks.Create(&db.Task{
		ID: taskID, Name: name, State: db.StateActive,
		Attention: db.AttentionOK, Cwd: workspaceDir, WorkspaceDir: workspaceDir,
	}); err != nil {
		t.Fatalf("creating task: %v", err)
	}
	return workspaceDir
}

func TestCreateSlotRecordsProvenance(t *testing.T) {
	f := newManagerFixture(t)
	initGitRepo(t, filepath.Join(f.reposDir, "alpha"))
	workspaceDir := slotTask(t, f, "01SLOT", "slots")

	result, err := f.manager.CreateSlot("01SLOT", "slots", workspaceDir, "alpha", "", "")
	if err != nil {
		t.Fatalf("CreateSlot: %v", err)
	}
	if result.Provenance.DirName != "alpha" || result.Provenance.VCSName != "slots" {
		t.Errorf("provenance = %+v, want the pre-slot names for an initial working copy", result.Provenance)
	}

	rows, err := f.workspaceRepos.ListByTask("01SLOT")
	if err != nil {
		t.Fatalf("listing workspace repos: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.RepoName != "alpha" || row.DirName != "alpha" || row.VCS != "git" || row.VCSName != "slots" {
		t.Errorf("row = %+v, want repo alpha / dir alpha / git / vcs name slots", row)
	}
	if row.SlotLabel != "" {
		t.Errorf("slot label = %q, want empty for an initial working copy", row.SlotLabel)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, "alpha", ".git")); err != nil {
		t.Errorf("expected a working copy at alpha: %v", err)
	}
}

func TestCreateSlotAutoNumbersSecondCopyOfRepo(t *testing.T) {
	f := newManagerFixture(t)
	initGitRepo(t, filepath.Join(f.reposDir, "alpha"))
	workspaceDir := slotTask(t, f, "01SLOT", "slots")

	if _, err := f.manager.CreateSlot("01SLOT", "slots", workspaceDir, "alpha", "", ""); err != nil {
		t.Fatalf("first CreateSlot: %v", err)
	}
	second, err := f.manager.CreateSlot("01SLOT", "slots", workspaceDir, "alpha", "", "")
	if err != nil {
		t.Fatalf("second CreateSlot: %v", err)
	}

	if second.Provenance.DirName != "alpha--2" || second.Provenance.VCSName != "slots--alpha--2" {
		t.Errorf("second slot provenance = %+v, want the 2 discriminator", second.Provenance)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, "alpha--2", ".git")); err != nil {
		t.Errorf("expected a second working copy at alpha--2: %v", err)
	}

	rows, err := f.workspaceRepos.ListByTask("01SLOT")
	if err != nil {
		t.Fatalf("listing workspace repos: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want one per working copy: %+v", len(rows), rows)
	}
	byDir := map[string]db.WorkspaceRepo{}
	for _, row := range rows {
		byDir[row.DirName] = row
	}
	if slot := byDir["alpha--2"]; slot.RepoName != "alpha" || slot.SlotLabel != "2" || slot.VCSName != "slots--alpha--2" {
		t.Errorf("second row = %+v, want repo alpha labeled 2", slot)
	}
}

func TestCreateSlotRefusesLabelAlreadyInUse(t *testing.T) {
	f := newManagerFixture(t)
	initGitRepo(t, filepath.Join(f.reposDir, "alpha"))
	workspaceDir := slotTask(t, f, "01SLOT", "slots")

	if _, err := f.manager.CreateSlot("01SLOT", "slots", workspaceDir, "alpha", "tests", ""); err != nil {
		t.Fatalf("first CreateSlot: %v", err)
	}

	_, err := f.manager.CreateSlot("01SLOT", "slots", workspaceDir, "alpha", "tests", "")
	if err == nil {
		t.Fatal("CreateSlot reused a slot label that already has a working copy")
	}
	if !strings.Contains(err.Error(), "alpha--tests") && !strings.Contains(err.Error(), "slots--alpha--tests") {
		t.Errorf("error %q should name the collision", err)
	}

	rows, _ := f.workspaceRepos.ListByTask("01SLOT")
	if len(rows) != 1 {
		t.Errorf("got %d rows, want the refused slot not to have been recorded: %+v", len(rows), rows)
	}
}

// AC (add --base): the revset the caller asked for reaches the VCS, and
// the row records it. base_revision used to be written empty always,
// which made "where did this slot start?" unanswerable after the
// bookmark moved.
func TestCreateSlotRecordsRequestedBase(t *testing.T) {
	f := newManagerFixture(t)
	repoDir := filepath.Join(f.reposDir, "alpha")
	initGitRepo(t, repoDir)

	firstCommit := gitRevParse(t, repoDir, "HEAD")
	runGit(t, repoDir, "commit", "--allow-empty", "-m", "second")

	workspaceDir := slotTask(t, f, "01SLOT", "slots")

	if _, err := f.manager.CreateSlot("01SLOT", "slots", workspaceDir, "alpha", "", firstCommit); err != nil {
		t.Fatalf("CreateSlot: %v", err)
	}

	if head := gitRevParse(t, filepath.Join(workspaceDir, "alpha"), "HEAD"); head != firstCommit {
		t.Errorf("slot HEAD = %s, want the requested base %s", head, firstCommit)
	}

	rows, _ := f.workspaceRepos.ListByTask("01SLOT")
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].BaseRevision != firstCommit {
		t.Errorf("base_revision = %q, want %q", rows[0].BaseRevision, firstCommit)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, output)
	}
	return strings.TrimSpace(string(output))
}

func gitRevParse(t *testing.T, dir, ref string) string {
	t.Helper()
	return runGit(t, dir, "rev-parse", ref)
}

func TestCreateSlotRejectsInvalidLabel(t *testing.T) {
	f := newManagerFixture(t)
	initGitRepo(t, filepath.Join(f.reposDir, "alpha"))
	workspaceDir := slotTask(t, f, "01SLOT", "slots")

	if _, err := f.manager.CreateSlot("01SLOT", "slots", workspaceDir, "alpha", "Not Valid", ""); err == nil {
		t.Fatal("CreateSlot accepted a label that can't be a branch name")
	}
	if entries, _ := os.ReadDir(workspaceDir); len(entries) != 0 {
		t.Errorf("workspace dir = %v, want nothing created for a rejected label", entries)
	}
}
