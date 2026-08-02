package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/hooks"
)

// renderable turns a workspace fixture's model into one a view function
// can be called on: the fixture builds the stores and the metarepo, not
// the presentation state, and a zero-width model renders nothing useful.
func renderable(m Model) Model {
	m.width = 140
	m.height = 44
	m.styles = BuildStyles(ClassicTheme)
	m.spinner = spinner.New(spinner.WithSpinner(spinner.MiniDot))
	m.pendingOps = map[string]string{}
	return m
}

func fixtureTask(t *testing.T, f *wsFixture) *db.Task {
	t.Helper()

	task, err := f.taskStore.Get("01ALPHA")
	if err != nil {
		t.Fatalf("loading the fixture task: %v", err)
	}
	return task
}

// modalLines strips a rendered modal's ANSI styling, box drawing, and
// padding so tests assert on the text and its order rather than on how
// wide the terminal happened to be.
func modalLines(view string) []string {
	raw := strings.Split(stripAnsi(view), "\n")
	lines := make([]string, len(raw))
	for i, line := range raw {
		lines[i] = strings.Trim(line, "│ ")
	}
	return lines
}

// lineIndex finds the one line that is exactly want, which is how a
// group heading is told apart from the slots listed under it.
func lineIndex(t *testing.T, lines []string, want string) int {
	t.Helper()

	for i, line := range lines {
		if line == want {
			return i
		}
	}
	t.Fatalf("no line reading exactly %q in:\n%s", want, strings.Join(lines, "\n"))
	return -1
}

// linePrefixIndex finds the first line starting with want.
func linePrefixIndex(t *testing.T, lines []string, want string) int {
	t.Helper()

	for i, line := range lines {
		if strings.HasPrefix(line, want) {
			return i
		}
	}
	t.Fatalf("no line starting with %q in:\n%s", want, strings.Join(lines, "\n"))
	return -1
}

// AC: a task's working copies are listed in the detail modal, grouped
// under the repo each one is a checkout of, with the slot label and the
// base each was created from.
func TestDetailModalGroupsSlotsUnderTheirRepo(t *testing.T) {
	f := newWSFixture(t, "alpha-repo", "beta-repo")

	base := strings.TrimSpace(runOut(t, filepath.Join(f.reposDir, "alpha-repo"), "git", "rev-parse", "HEAD"))
	f.add("alpha-repo", "")
	f.add("beta-repo", "")
	labeled := addRequest("alpha-repo", "tests")
	labeled.Base = base
	if resp := f.run(labeled); resp.Status != hooks.WorkspaceStatusOK {
		t.Fatalf("adding the labeled slot: %+v", resp)
	}

	m := renderable(f.model)
	lines := modalLines(m.renderDetailModal(fixtureTask(t, f)))
	view := strings.Join(lines, "\n")

	if !strings.Contains(view, "Working copies (3):") {
		t.Errorf("modal does not count the working copies:\n%s", view)
	}

	// Grouping: both slots of alpha-repo sit under its heading, before
	// beta-repo's group starts.
	alphaGroup := lineIndex(t, lines, "alpha-repo")
	initial := linePrefixIndex(t, lines, "· alpha-repo ")
	extra := linePrefixIndex(t, lines, "+ alpha-repo--tests")
	betaGroup := lineIndex(t, lines, "beta-repo")

	if !(alphaGroup < initial && initial < extra && extra < betaGroup) {
		t.Errorf("slots are not grouped under their repo (alpha=%d initial=%d extra=%d beta=%d):\n%s",
			alphaGroup, initial, extra, betaGroup, view)
	}

	// The label and the base are what distinguish an added slot from the
	// task's initial checkout of the same repo. A full object id is shown
	// in its short form, and the initial slot's detected default branch
	// symbolically, exactly as recorded.
	if !strings.Contains(lines[extra], "slot tests") {
		t.Errorf("the added slot does not name its label: %q", lines[extra])
	}
	if !strings.Contains(lines[extra], "base "+base[:12]) {
		t.Errorf("the added slot does not report base %q: %q", base, lines[extra])
	}
	if !strings.Contains(lines[initial], "initial") {
		t.Errorf("the initial working copy is not marked: %q", lines[initial])
	}
	if !strings.Contains(lines[initial], "base origin/") {
		t.Errorf("the initial working copy does not report its base: %q", lines[initial])
	}
}

// AC: a directory with no provenance row — a checkout somebody made by
// hand, or one caught mid-clone — is listed and marked, because what
// krang says about it was derived from its name rather than recorded.
func TestDetailModalMarksUnrecordedSlotDirectories(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	f.add("alpha-repo", "")

	handmade := filepath.Join(f.workspaceDir, "alpha-repo--handmade")
	if err := os.MkdirAll(filepath.Join(handmade, ".git"), 0o755); err != nil {
		t.Fatalf("faking a hand-made checkout: %v", err)
	}

	m := renderable(f.model)
	lines := modalLines(m.renderDetailModal(fixtureTask(t, f)))

	if !strings.Contains(strings.Join(lines, "\n"), "Working copies (2):") {
		t.Errorf("the unrecorded directory is missing from the count:\n%s", strings.Join(lines, "\n"))
	}

	// Both slots of the repo are grouped under it, and only the one with
	// no provenance row carries the mark.
	handmadeLine := lines[linePrefixIndex(t, lines, "+ alpha-repo--handmade")]
	if !strings.Contains(handmadeLine, "unrecorded") {
		t.Errorf("the hand-made checkout is not marked unrecorded: %q", handmadeLine)
	}
	initialLine := lines[linePrefixIndex(t, lines, "· alpha-repo ")]
	if strings.Contains(initialLine, "unrecorded") {
		t.Errorf("the recorded initial slot was marked unrecorded: %q", initialLine)
	}
}

// A recorded slot whose directory is gone is still listed, and says so,
// rather than quietly disappearing from the modal.
func TestDetailModalMarksMissingSlotDirectories(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")
	slot := f.add("alpha-repo", "")

	if err := os.RemoveAll(filepath.Join(f.workspaceDir, slot.Dir)); err != nil {
		t.Fatalf("removing the slot directory: %v", err)
	}

	m := renderable(f.model)
	lines := modalLines(m.renderDetailModal(fixtureTask(t, f)))

	slotLine := lines[linePrefixIndex(t, lines, "· alpha-repo ")]
	if !strings.Contains(slotLine, "missing") {
		t.Errorf("a recorded slot with nothing on disk was not marked missing: %q", slotLine)
	}
}

// A task with no workspace has no working copies to talk about, so the
// section is absent rather than empty.
func TestDetailModalOmitsWorkingCopiesForNonWorkspaceTasks(t *testing.T) {
	f := newWSFixture(t, "alpha-repo")

	task := fixtureTask(t, f)
	task.WorkspaceDir = ""

	m := renderable(f.model)
	if view := m.renderDetailModal(task); strings.Contains(view, "Working copies") {
		t.Errorf("a task with no workspace got a working-copies section:\n%s", view)
	}
}

// AC: a mutation an agent asked for over the API is as visible as one
// the human started, through the status line under the table.
func TestInFlightAgentRequestShowsInStatusLine(t *testing.T) {
	f := newWSFixture(t, "queue-repo")
	m := renderable(f.model)

	model, _ := m.Update(workspaceRequestMsg{Request: queuedRequest("tests")})
	m = model.(Model)
	if m.workspaceRequest == nil {
		t.Fatal("the request did not start")
	}

	view := m.View()
	for _, want := range []string{"agent", "add task=alpha", "queue-repo--tests"} {
		if !strings.Contains(view, want) {
			t.Errorf("the status line does not mention %q:\n%s", want, view)
		}
	}

	// The esc semantics are stated rather than left to be discovered.
	if !strings.Contains(view, "cannot cancel") {
		t.Errorf("the status line does not define its esc semantics:\n%s", view)
	}

	// It survives whatever the human happens to have open: the point of
	// a status line over a modal is that it does not take the screen.
	m.mode = ModeDetail
	m.tasks = []db.Task{*fixtureTask(t, f)}
	if !strings.Contains(m.View(), "add task=alpha") {
		t.Error("the status line vanished behind the human's detail modal")
	}
}

// A request that arrived while the human's own workspace flow was
// running says it is waiting, so a modal left open does not look like
// krang ignoring the agent.
func TestQueuedAgentRequestShowsInStatusLine(t *testing.T) {
	f := newWSFixture(t, "queue-repo")
	m := renderable(f.model)
	m.mode = ModeWorkspaceProgress
	m.wsProgress = &wsProgressState{Title: "Creating workspace \"beta\""}

	model, _ := m.Update(workspaceRequestMsg{Request: queuedRequest("waiting")})
	m = model.(Model)
	if m.workspaceRequest != nil {
		t.Fatal("the request started while the human's flow held the workspace")
	}

	if view := m.View(); !strings.Contains(view, "1 workspace request(s) queued") {
		t.Errorf("the queued request is not surfaced:\n%s", view)
	}
}

// Esc does not cancel an API-initiated mutation. That matches the W-key
// path, where esc stops the checklist before the *next* repo and has
// never interrupted a clone already underway — an API request is one
// operation with nothing behind it, so there is nothing to stop.
func TestEscDoesNotCancelAnInFlightAgentRequest(t *testing.T) {
	f := newWSFixture(t, "queue-repo")
	m := renderable(f.model)

	model, _ := m.Update(workspaceRequestMsg{Request: queuedRequest("tests")})
	m = model.(Model)

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(Model)

	if m.workspaceRequest == nil {
		t.Fatal("esc cancelled an in-flight API mutation")
	}
	if cmd != nil {
		t.Error("esc produced work while an API mutation was in flight")
	}
	if !strings.Contains(m.View(), "add task=alpha") {
		t.Error("esc dismissed the in-flight indicator without cancelling anything")
	}
}

// Finishing clears the indicator, so a stale spinner never outlives the
// work it was describing.
func TestStatusLineClearsWhenAgentRequestFinishes(t *testing.T) {
	f := newWSFixture(t, "queue-repo")
	m := renderable(f.model)

	req := queuedRequest("tests")
	model, cmd := m.Update(workspaceRequestMsg{Request: req})
	m = model.(Model)

	done := doneMsg(t, runCmd(t, cmd))
	model, _ = m.Update(done)
	m = model.(Model)

	if m.workspaceRequest != nil || m.workspaceRequestTask != "" || !m.workspaceRequestStarted.IsZero() {
		t.Errorf("in-flight display state survived completion: %+v %q %v",
			m.workspaceRequest, m.workspaceRequestTask, m.workspaceRequestStarted)
	}
	if status := m.renderWorkspaceRequestStatus(); status != "" {
		t.Errorf("the status line still shows %q after the request finished", status)
	}
}

// A caller that identified itself by cwd sends no task name, and the
// status line still has to say which task krang is changing.
func TestStatusLineNamesCwdIdentifiedTask(t *testing.T) {
	f := newWSFixture(t, "queue-repo")
	m := renderable(f.model)

	req := hooks.NewWorkspaceRequest(hooks.WorkspaceOpAdd, "")
	req.Cwd = f.workspaceDir
	req.Repo = "queue-repo"

	model, _ := m.Update(workspaceRequestMsg{Request: req})
	m = model.(Model)

	if m.workspaceRequestTask != "alpha" {
		t.Errorf("resolved task = %q, want alpha", m.workspaceRequestTask)
	}
	if status := m.renderWorkspaceRequestStatus(); !strings.Contains(status, "add task=alpha") {
		t.Errorf("status line = %q, want the resolved task name", status)
	}
}

// The debug log records the start of an agent's mutation, not only its
// completion: the human's flows announce themselves the moment they
// begin and this one has to as well.
func TestAgentRequestStartIsLogged(t *testing.T) {
	f := newWSFixture(t, "queue-repo")
	m := renderable(f.model)

	model, _ := m.Update(workspaceRequestMsg{Request: queuedRequest("tests")})
	m = model.(Model)

	if logged := strings.Join(m.debugLog, "\n"); !strings.Contains(logged, "workspace add task=alpha started") {
		t.Errorf("debug log %q has no start line", logged)
	}
}
