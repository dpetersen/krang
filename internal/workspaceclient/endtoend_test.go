package workspaceclient

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dpetersen/krang/internal/hooks"
)

// A live smoke test needs tmux, a TUI, and a real metarepo. This is the
// closest thing that runs in `go test`: the actual hooks.Server, started
// for real (dynamic port, state file on disk), with a stand-in for the
// Bubble Tea model servicing the request channel. Everything between the
// CLI's flags and the server's reply is production code — state file
// discovery, the endpoints, the envelopes, the queue hop, the timeout
// semantics — so the pieces are tested where they actually meet.
//
// The stand-in is deliberately not the real Model: that would drag in a
// database, a repo registry, and a filesystem full of working copies to
// test the CLI. What it does reproduce is the *shape* of the model's
// answers, which is the contract the CLI depends on.

// fakeTUI answers workspace requests the way the model does, over an
// in-memory workspace.
type fakeTUI struct {
	mu        sync.Mutex
	slots     []hooks.SlotInfo
	unsaved   map[string]bool
	stalledOp hooks.WorkspaceOp
}

const (
	fakeTask         = "alpha"
	fakeWorkspaceDir = "/ws/alpha"
)

// serve runs the request channel until it closes, exactly as
// Model.waitForWorkspaceRequest does one request at a time.
func (f *fakeTUI) serve(requests <-chan hooks.WorkspaceRequest) {
	for req := range requests {
		if req.Op == f.stalledOp {
			// Accepted and then never answered: the case the server turns
			// into applied "unknown".
			continue
		}
		req.Reply <- f.answer(req)
	}
}

func (f *fakeTUI) answer(req hooks.WorkspaceRequest) hooks.WorkspaceResponse {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.resolves(req) {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonUnknownTask, hooks.AppliedNo,
			fmt.Sprintf("no live task owns a workspace containing %q", req.Cwd))
	}

	var resp hooks.WorkspaceResponse
	switch req.Op {
	case hooks.WorkspaceOpList:
		resp = hooks.WorkspaceOK(req.Op, map[string]string{"workspace_dir": fakeWorkspaceDir, "strategy": "multi_repo"})
		resp.Slots = append([]hooks.SlotInfo(nil), f.slots...)

	case hooks.WorkspaceOpRepos:
		resp = hooks.WorkspaceOK(req.Op, map[string]string{"repos_dir": "/meta/repos"})
		resp.Repos = []hooks.RepoInfo{
			{Name: "beta", InTask: f.holds("beta"), Sets: []string{"core"}},
			{Name: "delta", InTask: f.holds("delta"), Sets: []string{}},
		}

	case hooks.WorkspaceOpAdd:
		if f.holds(req.Repo) && req.Label == "" {
			return hooks.WorkspaceFailure(req.Op, hooks.ReasonLabelRequired, hooks.AppliedNo,
				fmt.Sprintf("task %q already holds %q; name the new working copy with a label", fakeTask, req.Repo))
		}
		dir := req.Repo
		if req.Label != "" {
			dir = req.Repo + "--" + req.Label
		}
		base := req.Base
		if base == "" {
			base = "main@origin"
		}
		slot := hooks.SlotInfo{
			Dir: dir, Repo: req.Repo, Slot: req.Label, VCS: "jj",
			VCSName: fakeTask + "--" + dir, Base: base, Exists: true, Recorded: true,
		}
		f.slots = append(f.slots, slot)
		resp = hooks.WorkspaceOK(req.Op, map[string]string{
			"workspace_dir": fakeWorkspaceDir,
			"path":          fakeWorkspaceDir + "/" + dir,
		})
		resp.Slot = &slot

	case hooks.WorkspaceOpRemoveSlot:
		index := -1
		for i, slot := range f.slots {
			if slot.Dir == req.Dir || (req.Dir == "" && slot.Repo == req.Repo && slot.Slot == req.Label) {
				index = i
				break
			}
		}
		if index < 0 {
			return hooks.WorkspaceFailure(req.Op, hooks.ReasonUnknownSlot, hooks.AppliedNo,
				"no working copy in the workspace matches that")
		}
		target := f.slots[index]
		if f.unsaved[target.Dir] && !req.Force {
			failure := hooks.WorkspaceFailure(req.Op, hooks.ReasonUnsavedWork, hooks.AppliedNo,
				fmt.Sprintf("removing %q would lose work", target.Dir))
			failure.Blockers = []hooks.RemovalBlocker{
				{Dir: target.Dir, Kind: hooks.BlockerUnpushedCommits, Detail: "2 commits"},
			}
			return failure
		}
		f.slots = append(f.slots[:index], f.slots[index+1:]...)
		removed := target
		removed.Exists = false
		resp = hooks.WorkspaceOK(req.Op, map[string]string{
			"workspace_dir": fakeWorkspaceDir,
			"repo_dropped":  fmt.Sprintf("%t", !f.holds(target.Repo)),
		})
		resp.Slot = &removed

	default:
		resp = hooks.WorkspaceFailure(req.Op, hooks.ReasonUnsupportedOperation, hooks.AppliedNo, "not implemented")
	}

	resp.Task = fakeTask
	return resp
}

// resolves mirrors Model.resolveRequestTask closely enough to exercise
// the CLI's two ways of naming a task.
func (f *fakeTUI) resolves(req hooks.WorkspaceRequest) bool {
	if req.TaskName != "" {
		return req.TaskName == fakeTask
	}
	return req.Cwd == fakeWorkspaceDir || strings.HasPrefix(req.Cwd, fakeWorkspaceDir+"/")
}

func (f *fakeTUI) holds(repo string) bool {
	for _, slot := range f.slots {
		if slot.Repo == repo {
			return true
		}
	}
	return false
}

// liveHarness boots a real hooks.Server and returns a Runner that finds
// it exactly the way the CLI does: through KRANG_STATEFILE.
func liveHarness(t *testing.T, tui *fakeTUI, serverTimeout time.Duration) *harness {
	t.Helper()

	requests := make(chan hooks.WorkspaceRequest, 4)
	// The state file lives in the test's temp dir, so nothing here can
	// see — let alone disturb — a real instance's state under
	// ~/.local/state/krang.
	stateFilePath := filepath.Join(t.TempDir(), "krang-state.json")

	server := hooks.NewServer(stateFilePath, nil, requests)
	server.WorkspaceTimeout = serverTimeout
	if err := server.Start(); err != nil {
		t.Fatalf("starting the hook server: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		tui.serve(requests)
	}()
	t.Cleanup(func() {
		close(requests)
		<-done
	})
	t.Cleanup(server.Stop)

	h := &harness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	h.runner = Runner{
		Getenv:  func(key string) string { return map[string]string{StateFileEnv: stateFilePath}[key] },
		Getwd:   func() (string, error) { return fakeWorkspaceDir + "/beta", nil },
		Timeout: serverTimeout + time.Second,
		Stdout:  h.stdout,
		Stderr:  h.stderr,
	}
	return h
}

func (h *harness) reset() {
	h.stdout.Reset()
	h.stderr.Reset()
}

// GATE: the CLI client, driven against a real hooks.Server through a
// real state file, over a full add / list / refuse / force-remove
// sequence.
func TestEndToEndAgainstRealHookServer(t *testing.T) {
	tui := &fakeTUI{unsaved: map[string]bool{"beta--tests": true}}
	h := liveHarness(t, tui, 5*time.Second)
	ctx := context.Background()

	// Argument-free, from inside the workspace: the first call an agent
	// makes, before it knows what krang calls this task.
	if code := h.runner.Run(ctx, Request{Op: hooks.WorkspaceOpList}); code != ExitOK {
		t.Fatalf("list exited %d, want 0\nstderr: %s", code, h.stderr)
	}
	if !strings.Contains(h.stdout.String(), "no working copies") {
		t.Errorf("first listing = %q, want it to report an empty workspace", h.stdout.String())
	}

	h.reset()
	if code := h.runner.Run(ctx, Request{Op: hooks.WorkspaceOpRepos}); code != ExitOK {
		t.Fatalf("repos exited %d, want 0\nstderr: %s", code, h.stderr)
	}
	if !strings.Contains(h.stdout.String(), "beta") || !strings.Contains(h.stdout.String(), "core") {
		t.Errorf("repos = %q, want the available repo and its set", h.stdout.String())
	}

	h.reset()
	if code := h.runner.Run(ctx, Request{Op: hooks.WorkspaceOpAdd, Params: Params{Repo: "beta"}}); code != ExitOK {
		t.Fatalf("add exited %d, want 0\nstderr: %s", code, h.stderr)
	}
	if h.stdout.String() != fakeWorkspaceDir+"/beta\n" {
		t.Errorf("add stdout = %q, want just the new absolute path", h.stdout.String())
	}

	// A second working copy of a repo the task now holds must be named.
	h.reset()
	if code := h.runner.Run(ctx, Request{Op: hooks.WorkspaceOpAdd, Params: Params{Repo: "beta"}}); code != ExitRefused {
		t.Fatalf("unlabelled second add exited %d, want %d\nstderr: %s", code, ExitRefused, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), hooks.ReasonLabelRequired) {
		t.Errorf("stderr = %q, want the label_required reason", h.stderr.String())
	}

	h.reset()
	if code := h.runner.Run(ctx, Request{Op: hooks.WorkspaceOpAdd, Params: Params{
		Repo: "beta", Label: "tests", Base: "main@origin",
	}}); code != ExitOK {
		t.Fatalf("labelled add exited %d, want 0\nstderr: %s", code, h.stderr)
	}
	if h.stdout.String() != fakeWorkspaceDir+"/beta--tests\n" {
		t.Errorf("labelled add stdout = %q, want the slot path", h.stdout.String())
	}

	h.reset()
	if code := h.runner.Run(ctx, Request{Op: hooks.WorkspaceOpList, Params: Params{Task: fakeTask}}); code != ExitOK {
		t.Fatalf("list by task exited %d, want 0\nstderr: %s", code, h.stderr)
	}
	listing := h.stdout.String()
	if !strings.Contains(listing, "beta--tests") || !strings.Contains(listing, "main@origin") {
		t.Errorf("listing = %q, want both working copies with their base", listing)
	}

	// The slot has unpushed work, so the removal is refused with
	// something the caller can act on.
	h.reset()
	if code := h.runner.Run(ctx, Request{Op: hooks.WorkspaceOpRemoveSlot, Params: Params{Dir: "beta--tests"}}); code != ExitRefused {
		t.Fatalf("removal exited %d, want %d\nstderr: %s", code, ExitRefused, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), hooks.BlockerUnpushedCommits) {
		t.Errorf("stderr = %q, want the blocker named", h.stderr.String())
	}

	h.reset()
	if code := h.runner.Run(ctx, Request{Op: hooks.WorkspaceOpRemoveSlot, Params: Params{
		Dir: "beta--tests", Force: true,
	}}); code != ExitOK {
		t.Fatalf("forced removal exited %d, want 0\nstderr: %s", code, h.stderr)
	}
	if !strings.Contains(h.stdout.String(), "removed beta--tests") {
		t.Errorf("stdout = %q, want the removal reported", h.stdout.String())
	}

	h.reset()
	if code := h.runner.Run(ctx, Request{Op: hooks.WorkspaceOpList, JSON: true}); code != ExitOK {
		t.Fatalf("final list exited %d, want 0\nstderr: %s", code, h.stderr)
	}
	final := h.stdout.String()
	if strings.Contains(final, "beta--tests") {
		t.Errorf("final listing still has the removed working copy: %s", final)
	}
	if !strings.Contains(final, `"slots"`) || !strings.Contains(final, `"recorded"`) {
		t.Errorf("--json output = %q, want the raw envelope", final)
	}
}

// The real server's timeout path, end to end: the TUI takes the request
// and never answers, so the CLI must report "might have applied" rather
// than a dead connection.
func TestEndToEndServerTimeoutExitsUnknown(t *testing.T) {
	tui := &fakeTUI{stalledOp: hooks.WorkspaceOpAdd}
	h := liveHarness(t, tui, 200*time.Millisecond)

	code := h.runner.Run(context.Background(), Request{Op: hooks.WorkspaceOpAdd, Params: Params{Repo: "beta"}})

	if code != ExitUnknown {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUnknown, h.stderr)
	}
	stderr := h.stderr.String()
	if !strings.Contains(stderr, hooks.ReasonTimeout) || !strings.Contains(stderr, hooks.AppliedUnknown) {
		t.Errorf("stderr = %q, want the timeout reason and applied:unknown", stderr)
	}
	if !strings.Contains(stderr, "DO NOT blindly retry") {
		t.Errorf("stderr = %q, want the warning against retrying", stderr)
	}
}

// A request nothing ever accepts: the server answers not_accepted, which
// is the one case with a hard guarantee that nothing ran.
func TestEndToEndUnacceptedRequestExitsUnavailable(t *testing.T) {
	stateFilePath := filepath.Join(t.TempDir(), "krang-state.json")
	// An unbuffered channel with no reader: the handler can never hand
	// the request over.
	server := hooks.NewServer(stateFilePath, nil, make(chan hooks.WorkspaceRequest))
	server.WorkspaceTimeout = 200 * time.Millisecond
	if err := server.Start(); err != nil {
		t.Fatalf("starting the hook server: %v", err)
	}
	t.Cleanup(server.Stop)

	h := &harness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	h.runner = Runner{
		Getenv: func(key string) string { return map[string]string{StateFileEnv: stateFilePath}[key] },
		Getwd:  func() (string, error) { return fakeWorkspaceDir, nil },
		Stdout: h.stdout, Stderr: h.stderr,
		Timeout: 3 * time.Second,
	}

	code := h.runner.Run(context.Background(), Request{Op: hooks.WorkspaceOpAdd, Params: Params{Repo: "beta"}})

	if code != ExitUnavailable {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitUnavailable, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "Nothing was applied") {
		t.Errorf("stderr = %q, want the guarantee that nothing ran", h.stderr.String())
	}
}
