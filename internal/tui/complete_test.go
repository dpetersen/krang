package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dpetersen/krang/internal/db"
)

// confirmComplete drives the keyboard path a human takes to the
// completion confirmation: the detail modal, then "c". Going through
// Update rather than calling the handler keeps the warnings the modal
// renders wired to the keystroke that gathers them.
func confirmComplete(t *testing.T, f *wsFixture) Model {
	t.Helper()

	m := renderable(f.model)
	m.tasks = []db.Task{*fixtureTask(t, f)}
	m.cursor = 0
	m.mode = ModeDetail

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = model.(Model)
	if m.mode != ModeConfirmComplete {
		t.Fatalf("mode = %v after pressing c, want the completion confirmation", m.mode)
	}
	return m
}

// modalText flattens a rendered modal to a single whitespace-normalised
// line, so an assertion is about the sentence krang wrote rather than
// where lipgloss happened to wrap a temp-directory path.
func modalText(view string) string {
	return strings.Join(strings.Fields(strings.Join(modalLines(view), " ")), " ")
}

// pressY answers the confirmation and returns the model plus whatever
// the answer set in motion.
func pressY(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	return model.(Model), cmd
}

// AC: the confirmation states how many working copies are about to go,
// slots included. "The workspace" alone stopped being an answer once a
// task could hold three checkouts of one repo.
func TestCompleteModalCountsEveryWorkingCopy(t *testing.T) {
	f := newWSFixture(t, "alpha-repo", "beta-repo")
	f.add("alpha-repo", "")
	f.add("beta-repo", "")
	f.add("alpha-repo", "tests")

	m := confirmComplete(t, f)
	if m.confirmWorkingCopies != 3 {
		t.Errorf("counted %d working copies, want 3", m.confirmWorkingCopies)
	}

	view := modalText(m.renderConfirmComplete(fixtureTask(t, f)))
	if !strings.Contains(view, "(3 working copies) will be deleted") {
		t.Errorf("the modal does not count the working copies:\n%s", view)
	}
}

// One checkout reads as one, not "1 working copies".
func TestCompleteModalCountsASingleWorkingCopyInTheSingular(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")

	m := confirmComplete(t, f)
	view := modalText(m.renderConfirmComplete(fixtureTask(t, f)))
	if !strings.Contains(view, "(1 working copy) will be deleted") {
		t.Errorf("the modal does not count one working copy in the singular:\n%s", view)
	}
}

// AC: a slot holding unsaved work is named, and named as itself — the
// directory that holds it and the branch cleanup will leave behind. Both
// used to be reported as the task, which for a slot is neither.
func TestCompleteModalNamesTheSlotHoldingUnsavedWork(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")
	slot := f.add("alpha-repo", "tests")

	slotPath := filepath.Join(f.workspaceDir, slot.Dir)
	if err := os.WriteFile(filepath.Join(slotPath, "committed.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatalf("writing a file in the slot: %v", err)
	}
	mustRun(t, slotPath, "git", "add", "committed.txt")
	mustRun(t, slotPath, "git", "commit", "-m", "unpushed work")
	if err := os.WriteFile(filepath.Join(slotPath, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("dirtying the slot: %v", err)
	}

	m := confirmComplete(t, f)

	if len(m.confirmUncommitted) != 1 || m.confirmUncommitted[0].Dir != "alpha-repo--tests" {
		t.Fatalf("uncommitted = %+v, want only the slot", m.confirmUncommitted)
	}
	if len(m.confirmUnpushed) != 1 || m.confirmUnpushed[0].Dir != "alpha-repo--tests" {
		t.Fatalf("unpushed = %+v, want only the slot", m.confirmUnpushed)
	}

	// The branch is the slot's own, not krang/<task>: that is the branch
	// "git branch -d" will decline to delete, so it is the branch worth
	// telling the human survives.
	wantBranch := "krang/alpha--alpha-repo--tests"
	if got := m.confirmUnpushed[0].Branch; got != wantBranch {
		t.Errorf("branch = %q, want %q", got, wantBranch)
	}

	view := modalText(m.renderConfirmComplete(fixtureTask(t, f)))
	for _, want := range []string{
		"Uncommitted changes will be lost:",
		"alpha-repo--tests",
		wantBranch,
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the modal does not mention %q:\n%s", want, view)
		}
	}
	// The task's own branch belongs to the initial checkout, which has
	// nothing to lose, so it must not be the one attributed to the slot.
	if strings.Contains(view, "(krang/alpha)") {
		t.Errorf("the modal names the task's branch for a slot's work:\n%s", view)
	}
}

// AC: confirming queues one destruction per working copy, each carrying
// the identity krang recorded for it. A per-repo loop would forget the
// first checkout of alpha-repo twice and the second never.
func TestCompleteQueuesEveryRecordedSlotForDestruction(t *testing.T) {
	f := newWSFixture(t, "alpha-repo", "beta-repo")
	f.add("alpha-repo", "")
	f.add("beta-repo", "")
	f.add("alpha-repo", "tests")

	m, _ := pressY(t, confirmComplete(t, f))

	if m.mode != ModeWorkspaceProgress || m.wsProgress == nil {
		t.Fatalf("mode = %v / progress = %v, want the destroy checklist", m.mode, m.wsProgress)
	}
	if !m.wsProgress.Destroying {
		t.Error("the checklist is not marked as a destroy")
	}

	var dirs, identities []string
	for _, entry := range m.wsProgress.Repos {
		dirs = append(dirs, entry.Repo)
		identities = append(identities, entry.Provenance.VCSName)
	}
	wantDirs := []string{"alpha-repo", "beta-repo", "alpha-repo--tests"}
	wantIdentities := []string{"alpha", "alpha", "alpha--alpha-repo--tests"}
	if strings.Join(dirs, ",") != strings.Join(wantDirs, ",") {
		t.Errorf("checklist = %v, want one entry per working copy %v", dirs, wantDirs)
	}
	if strings.Join(identities, ",") != strings.Join(wantIdentities, ",") {
		t.Errorf("identities = %v, want %v", identities, wantIdentities)
	}
	for i, entry := range m.wsProgress.Repos {
		if !entry.Provenance.Recorded {
			t.Errorf("entry %d (%s) is not using its recorded row", i, entry.Repo)
		}
	}
}

// AC: completing a fork that shares its owner's workspace directory
// destroys nothing. Forking was deliberately left non-slot-aware, so the
// only thing standing between a fork's completion and the owner's slots
// is this suppression — including the owner's *labeled* slots, which the
// fork never had any claim on.
func TestCompletingAForkLeavesTheSharedWorkspaceAlone(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")
	slot := f.add("alpha-repo", "tests")

	// A fork sharing the owner's workspace: same directory, its own task.
	if err := f.taskStore.Create(&db.Task{
		ID: "01FORK", Name: "alpha-fork", State: db.StateActive, Attention: db.AttentionOK,
		Cwd: f.workspaceDir, WorkspaceDir: f.workspaceDir, SourceTaskID: "01ALPHA",
	}); err != nil {
		t.Fatalf("creating the fork: %v", err)
	}
	fork, err := f.taskStore.Get("01FORK")
	if err != nil || fork == nil {
		t.Fatalf("loading the fork: %v", err)
	}

	m := renderable(f.model)
	m.tasks = []db.Task{*fork}
	m.cursor = 0
	m.mode = ModeDetail

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = model.(Model)

	// The confirmation says outright that the directory survives, naming
	// the task it belongs to.
	view := modalText(m.renderConfirmComplete(fork))
	for _, want := range []string{"shared with alpha", "will NOT be deleted"} {
		if !strings.Contains(view, want) {
			t.Errorf("the modal does not warn %q:\n%s", want, view)
		}
	}

	m, cmd := pressY(t, m)
	if m.mode == ModeWorkspaceProgress || m.wsProgress != nil {
		t.Fatalf("completing a fork started a destroy: mode %v, progress %v", m.mode, m.wsProgress)
	}
	runCmd(t, cmd)

	// Every one of the owner's working copies is still there, labeled
	// slot included, and so is the provenance that will let the owner's
	// own completion forget them.
	for _, dir := range []string{"alpha-repo", slot.Dir} {
		if _, err := os.Stat(filepath.Join(f.workspaceDir, dir)); err != nil {
			t.Errorf("the owner's %s was destroyed by the fork's completion: %v", dir, err)
		}
	}
	rows, err := f.repoRows.ListByTask("01ALPHA")
	if err != nil {
		t.Fatalf("listing the owner's rows: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("the owner holds %d provenance rows, want 2: %+v", len(rows), rows)
	}

	// And the jj/git identities are still claimed, since the working
	// copies they name are still checked out.
	branches := mustRun(t, filepath.Join(f.reposDir, "alpha-repo"), "git", "branch", "--list")
	for _, branch := range []string{"krang/alpha", "krang/alpha--alpha-repo--tests"} {
		if !strings.Contains(branches, branch) {
			t.Errorf("branch %s was deleted by the fork's completion:\n%s", branch, branches)
		}
	}
}
