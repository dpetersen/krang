package workspaceclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dpetersen/krang/internal/hooks"
)

// The tests drive the whole CLI path — state file, transport, envelope,
// rendering, exit code — because that whole path is what a cobra binding
// delegates to. Anything short of it would be testing a layer no caller
// uses on its own.

const testCwd = "/ws/alpha/beta"

type recordedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Body   string
}

type harness struct {
	runner   Runner
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
	mu       sync.Mutex
	requests []recordedRequest
}

func (h *harness) record(r recordedRequest) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests = append(h.requests, r)
}

func (h *harness) seen() []recordedRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]recordedRequest(nil), h.requests...)
}

func (h *harness) run(t *testing.T, req Request) int {
	t.Helper()
	return h.runner.Run(context.Background(), req)
}

// writeStateFile puts a state file on disk the way a running krang
// instance does, so the tests exercise the real discovery path rather
// than injecting a base URL past it.
func writeStateFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "krang-state.json")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing state file: %v", err)
	}
	return path
}

func stateFileForURL(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing %q: %v", rawURL, err)
	}
	return writeStateFile(t, fmt.Sprintf(`{"port":%s}`, parsed.Port()))
}

// newHarnessWithStateFile points a Runner at a state file with no
// server behind it, which is how the "krang isn't there" cases are set
// up.
func newHarnessWithStateFile(t *testing.T, stateFilePath string) *harness {
	t.Helper()
	h := &harness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	h.runner = Runner{
		Getenv:  func(key string) string { return map[string]string{StateFileEnv: stateFilePath}[key] },
		Getwd:   func() (string, error) { return testCwd, nil },
		Timeout: 3 * time.Second,
		Stdout:  h.stdout,
		Stderr:  h.stderr,
	}
	return h
}

// newHarness stands up an httptest server for handler and points a
// Runner at it through a real state file.
func newHarness(t *testing.T, handler http.HandlerFunc) *harness {
	t.Helper()
	h := &harness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		h.record(recordedRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Body: body.String()})
		handler(w, r)
	}))
	t.Cleanup(ts.Close)

	h.runner = Runner{
		Getenv:  func(key string) string { return map[string]string{StateFileEnv: stateFileForURL(t, ts.URL)}[key] },
		Getwd:   func() (string, error) { return testCwd, nil },
		Timeout: 3 * time.Second,
		Stdout:  h.stdout,
		Stderr:  h.stderr,
	}
	return h
}

// respondWith answers every request with the same envelope.
func respondWith(status int, resp hooks.WorkspaceResponse) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func okEnvelope(op hooks.WorkspaceOp) hooks.WorkspaceResponse {
	resp := hooks.WorkspaceOK(op, map[string]string{"workspace_dir": "/ws/alpha"})
	resp.Task = "alpha"
	return resp
}

func decodeBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("request body %q is not JSON: %v", raw, err)
	}
	return body
}

func wantExit(t *testing.T, got, want int, h *harness) {
	t.Helper()
	if got != want {
		t.Errorf("exit = %d, want %d\nstdout: %s\nstderr: %s", got, want, h.stdout, h.stderr)
	}
}

// --- naming the task ---

// AC: `krang workspace list` has to work with no arguments at all from
// inside a task, because it is the first call an agent makes and the
// only one it can make without already knowing something.
func TestListWithNoArgumentsSendsTheProcessCwd(t *testing.T) {
	h := newHarness(t, respondWith(http.StatusOK, okEnvelope(hooks.WorkspaceOpList)))

	code := h.run(t, Request{Op: hooks.WorkspaceOpList})

	wantExit(t, code, ExitOK, h)
	seen := h.seen()
	if len(seen) != 1 {
		t.Fatalf("made %d requests, want 1", len(seen))
	}
	if seen[0].Method != http.MethodGet || seen[0].Path != "/api/workspace" {
		t.Errorf("called %s %s, want GET /api/workspace", seen[0].Method, seen[0].Path)
	}
	if got := seen[0].Query.Get("cwd"); got != testCwd {
		t.Errorf("cwd = %q, want the process cwd %q", got, testCwd)
	}
	if got := seen[0].Query.Get("task"); got != "" {
		t.Errorf("task = %q, want it unset when the caller named none", got)
	}
}

// --task overrides, and the cwd is dropped rather than sent alongside:
// two answers to "which task?" in one request is one too many.
func TestTaskFlagReplacesTheCwdDefault(t *testing.T) {
	h := newHarness(t, respondWith(http.StatusOK, okEnvelope(hooks.WorkspaceOpList)))

	code := h.run(t, Request{Op: hooks.WorkspaceOpList, Params: Params{Task: "gamma"}})

	wantExit(t, code, ExitOK, h)
	query := h.seen()[0].Query
	if query.Get("task") != "gamma" {
		t.Errorf("task = %q, want gamma", query.Get("task"))
	}
	if query.Has("cwd") {
		t.Errorf("cwd = %q, want it omitted when --task was given", query.Get("cwd"))
	}
}

func TestExplicitCwdIsSentUnchanged(t *testing.T) {
	h := newHarness(t, respondWith(http.StatusOK, okEnvelope(hooks.WorkspaceOpRepos)))

	code := h.run(t, Request{Op: hooks.WorkspaceOpRepos, Params: Params{Cwd: "/elsewhere"}})

	wantExit(t, code, ExitOK, h)
	if got := h.seen()[0].Query.Get("cwd"); got != "/elsewhere" {
		t.Errorf("cwd = %q, want /elsewhere", got)
	}
}

// --- list ---

func TestListRendersOneRowPerWorkingCopy(t *testing.T) {
	resp := okEnvelope(hooks.WorkspaceOpList)
	resp.Slots = []hooks.SlotInfo{
		{Dir: "alpha", Repo: "alpha", VCS: "jj", VCSName: "task", Base: "main@origin", Exists: true, Recorded: true},
		{Dir: "beta--tests", Repo: "beta", Slot: "tests", VCS: "git", Base: "main", Exists: false, Recorded: true},
		{Dir: "gamma", Repo: "gamma", VCS: "git", Exists: true, Recorded: false},
	}
	h := newHarness(t, respondWith(http.StatusOK, resp))

	code := h.run(t, Request{Op: hooks.WorkspaceOpList})

	wantExit(t, code, ExitOK, h)
	out := h.stdout.String()
	for _, want := range []string{"DIR", "REPO", "SLOT", "BASE", "STATE", "alpha", "beta--tests", "tests", "gamma"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing is missing %q:\n%s", want, out)
		}
	}
	// The three states are the whole point of the column: a caller has to
	// be able to tell a healthy slot from a recorded-but-gone one from a
	// directory krang is only guessing about.
	for _, want := range []string{"ok", "missing", "unrecorded"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing never says %q:\n%s", want, out)
		}
	}
}

func TestListWithNoWorkingCopiesSaysSoRatherThanPrintingNothing(t *testing.T) {
	h := newHarness(t, respondWith(http.StatusOK, okEnvelope(hooks.WorkspaceOpList)))

	code := h.run(t, Request{Op: hooks.WorkspaceOpList})

	wantExit(t, code, ExitOK, h)
	out := h.stdout.String()
	if !strings.Contains(out, "no working copies") || !strings.Contains(out, "alpha") {
		t.Errorf("empty listing = %q, want it to name the task and say it holds nothing", out)
	}
}

// --- repos ---

func TestReposRendersAvailabilityAndSets(t *testing.T) {
	resp := hooks.WorkspaceOK(hooks.WorkspaceOpRepos, map[string]string{"repos_dir": "/meta/repos"})
	resp.Task = "alpha"
	resp.Repos = []hooks.RepoInfo{
		{Name: "alpha", InTask: true, Sets: []string{"core"}},
		{Name: "beta", InTask: false, Sets: []string{}},
	}
	h := newHarness(t, respondWith(http.StatusOK, resp))

	code := h.run(t, Request{Op: hooks.WorkspaceOpRepos})

	wantExit(t, code, ExitOK, h)
	if h.seen()[0].Path != "/api/workspace/repos" {
		t.Errorf("called %s, want /api/workspace/repos", h.seen()[0].Path)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "yes") || !strings.Contains(out, "core") {
		t.Errorf("repos listing = %q, want the held repo marked and its set named", out)
	}
	if !strings.Contains(out, "beta") || !strings.Contains(out, "no") {
		t.Errorf("repos listing = %q, want the unheld repo listed too", out)
	}
}

// --- add ---

// AC: add prints the new absolute path on stdout and nothing else, so
// the next command can consume it without parsing a sentence.
func TestAddPrintsOnlyTheNewAbsolutePath(t *testing.T) {
	resp := hooks.WorkspaceOK(hooks.WorkspaceOpAdd, map[string]string{
		"workspace_dir": "/ws/alpha",
		"path":          "/ws/alpha/beta--tests",
	})
	resp.Task = "alpha"
	resp.Slot = &hooks.SlotInfo{Dir: "beta--tests", Repo: "beta", Slot: "tests", Exists: true, Recorded: true}
	h := newHarness(t, respondWith(http.StatusOK, resp))

	code := h.run(t, Request{Op: hooks.WorkspaceOpAdd, Params: Params{Repo: "beta", Label: "tests"}})

	wantExit(t, code, ExitOK, h)
	if h.stdout.String() != "/ws/alpha/beta--tests\n" {
		t.Errorf("stdout = %q, want just the new path", h.stdout.String())
	}
}

func TestAddPostsRepoLabelAndBase(t *testing.T) {
	h := newHarness(t, respondWith(http.StatusOK, okEnvelope(hooks.WorkspaceOpAdd)))

	code := h.run(t, Request{Op: hooks.WorkspaceOpAdd, Params: Params{
		Repo: "beta", Label: "tests", Base: "main@origin",
	}})

	wantExit(t, code, ExitOK, h)
	seen := h.seen()[0]
	if seen.Method != http.MethodPost || seen.Path != "/api/workspace/add" {
		t.Errorf("called %s %s, want POST /api/workspace/add", seen.Method, seen.Path)
	}
	body := decodeBody(t, seen.Body)
	for key, want := range map[string]string{"repo": "beta", "label": "tests", "base": "main@origin", "cwd": testCwd} {
		if body[key] != want {
			t.Errorf("body[%q] = %v, want %q", key, body[key], want)
		}
	}
}

// The server would refuse this too, but a round trip to be told "--repo
// is required" is a round trip an agent waits on for nothing.
func TestAddWithoutRepoFailsLocallyWithoutCallingKrang(t *testing.T) {
	h := newHarness(t, respondWith(http.StatusOK, okEnvelope(hooks.WorkspaceOpAdd)))

	code := h.run(t, Request{Op: hooks.WorkspaceOpAdd})

	wantExit(t, code, ExitError, h)
	if len(h.seen()) != 0 {
		t.Errorf("made %d requests, want none", len(h.seen()))
	}
	if !strings.Contains(h.stderr.String(), "--repo") {
		t.Errorf("stderr = %q, want it to name the missing flag", h.stderr.String())
	}
}

// --- remove ---

func TestRemoveSendsTheSlotNameAndForce(t *testing.T) {
	resp := hooks.WorkspaceOK(hooks.WorkspaceOpRemoveSlot, map[string]string{"repo_dropped": "false"})
	resp.Task = "alpha"
	resp.Slot = &hooks.SlotInfo{Dir: "beta--tests", Repo: "beta", Slot: "tests"}
	h := newHarness(t, respondWith(http.StatusOK, resp))

	code := h.run(t, Request{Op: hooks.WorkspaceOpRemoveSlot, Params: Params{Dir: "beta--tests", Force: true}})

	wantExit(t, code, ExitOK, h)
	seen := h.seen()[0]
	if seen.Method != http.MethodDelete || seen.Path != "/api/workspace/slot" {
		t.Errorf("called %s %s, want DELETE /api/workspace/slot", seen.Method, seen.Path)
	}
	body := decodeBody(t, seen.Body)
	if body["dir"] != "beta--tests" || body["force"] != true {
		t.Errorf("body = %v, want the slot dir and force", body)
	}
}

func TestRemoveReportsWhatWentAndWhetherTheRepoLeft(t *testing.T) {
	resp := hooks.WorkspaceOK(hooks.WorkspaceOpRemoveSlot, map[string]string{"repo_dropped": "true"})
	resp.Task = "alpha"
	resp.Slot = &hooks.SlotInfo{Dir: "beta", Repo: "beta"}
	h := newHarness(t, respondWith(http.StatusOK, resp))

	code := h.run(t, Request{Op: hooks.WorkspaceOpRemoveSlot, Params: Params{Repo: "beta"}})

	wantExit(t, code, ExitOK, h)
	out := h.stdout.String()
	if !strings.Contains(out, "removed beta") || !strings.Contains(out, "alpha") {
		t.Errorf("stdout = %q, want it to name the working copy and the task", out)
	}
	if !strings.Contains(out, "no longer holds") {
		t.Errorf("stdout = %q, want the repo_dropped note", out)
	}
}

func TestRemoveWithoutASlotNameFailsLocally(t *testing.T) {
	h := newHarness(t, respondWith(http.StatusOK, okEnvelope(hooks.WorkspaceOpRemoveSlot)))

	code := h.run(t, Request{Op: hooks.WorkspaceOpRemoveSlot})

	wantExit(t, code, ExitError, h)
	if len(h.seen()) != 0 {
		t.Errorf("made %d requests, want none", len(h.seen()))
	}
	stderr := h.stderr.String()
	if !strings.Contains(stderr, "--dir") || !strings.Contains(stderr, "--repo") {
		t.Errorf("stderr = %q, want both ways of naming a working copy", stderr)
	}
}

// --- --json ---

// AC: --json prints the envelope krang sent, not a re-encoding of it.
// The unknown field proves it: a round trip through our own structs
// would drop it.
func TestJSONPrintsTheEnvelopeVerbatim(t *testing.T) {
	const raw = `{"status":"ok","op":"list","task":"alpha","slots":[],"future_field":"kept"}`
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(raw))
	})

	code := h.run(t, Request{Op: hooks.WorkspaceOpList, JSON: true})

	wantExit(t, code, ExitOK, h)
	if h.stdout.String() != raw+"\n" {
		t.Errorf("stdout = %q, want the response bytes unchanged", h.stdout.String())
	}
}

// --json still exits on the code the envelope implies, and the warning
// goes to stderr where it can't corrupt the JSON on stdout.
func TestJSONOnFailurePrintsEnvelopeAndStillExitsOnTheReason(t *testing.T) {
	h := newHarness(t, respondWith(http.StatusConflict,
		hooks.WorkspaceFailure(hooks.WorkspaceOpRemoveSlot, hooks.ReasonUnsavedWork, hooks.AppliedNo, "would lose work")))

	code := h.run(t, Request{Op: hooks.WorkspaceOpRemoveSlot, Params: Params{Dir: "beta"}, JSON: true})

	wantExit(t, code, ExitRefused, h)
	var envelope hooks.WorkspaceResponse
	if err := json.Unmarshal(h.stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("stdout is not the envelope: %v (%q)", err, h.stdout.String())
	}
	if envelope.Reason != hooks.ReasonUnsavedWork {
		t.Errorf("stdout envelope reason = %q, want %q", envelope.Reason, hooks.ReasonUnsavedWork)
	}
	if !strings.Contains(h.stderr.String(), "would lose work") {
		t.Errorf("stderr = %q, want the human half of the refusal", h.stderr.String())
	}
}

// --- refusals and exit codes ---

func TestUnsavedWorkRefusalExitsRefusedAndNamesTheBlockers(t *testing.T) {
	resp := hooks.WorkspaceFailure(hooks.WorkspaceOpRemoveSlot, hooks.ReasonUnsavedWork, hooks.AppliedNo,
		`removing "beta" would lose work`)
	resp.Blockers = []hooks.RemovalBlocker{{Dir: "beta", Kind: hooks.BlockerUnpushedCommits, Detail: "2 commits"}}
	h := newHarness(t, respondWith(http.StatusConflict, resp))

	code := h.run(t, Request{Op: hooks.WorkspaceOpRemoveSlot, Params: Params{Dir: "beta"}})

	wantExit(t, code, ExitRefused, h)
	stderr := h.stderr.String()
	for _, want := range []string{"would lose work", hooks.ReasonUnsavedWork, hooks.BlockerUnpushedCommits, "2 commits", "identical command"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
	if h.stdout.Len() != 0 {
		t.Errorf("stdout = %q, want failures kept off stdout", h.stdout.String())
	}
}

func TestLabelRequiredExitsRefused(t *testing.T) {
	h := newHarness(t, respondWith(http.StatusBadRequest,
		hooks.WorkspaceFailure(hooks.WorkspaceOpAdd, hooks.ReasonLabelRequired, hooks.AppliedNo,
			`task "alpha" already holds "beta"; try --label 2`)))

	code := h.run(t, Request{Op: hooks.WorkspaceOpAdd, Params: Params{Repo: "beta"}})

	wantExit(t, code, ExitRefused, h)
	if !strings.Contains(h.stderr.String(), "--label 2") {
		t.Errorf("stderr = %q, want the suggested label passed through", h.stderr.String())
	}
}

// AC: 503 with applied "unknown" gets its own code and says outright not
// to retry. This is the one failure the exit codes exist to prevent.
func TestTimeoutWithUnknownAppliedExitsUnknownAndForbidsBlindRetry(t *testing.T) {
	h := newHarness(t, respondWith(http.StatusServiceUnavailable,
		hooks.WorkspaceFailure(hooks.WorkspaceOpAdd, hooks.ReasonTimeout, hooks.AppliedUnknown,
			"krang did not finish add within 60s")))

	code := h.run(t, Request{Op: hooks.WorkspaceOpAdd, Params: Params{Repo: "beta"}})

	wantExit(t, code, ExitUnknown, h)
	stderr := h.stderr.String()
	if !strings.Contains(stderr, "DO NOT blindly retry") {
		t.Errorf("stderr = %q, want an explicit warning against retrying", stderr)
	}
	if !strings.Contains(stderr, "krang workspace list") {
		t.Errorf("stderr = %q, want it to name the command that resolves the ambiguity", stderr)
	}
}

// applied beats reason: a mutation that failed partway is still a
// mutation that may have landed, whatever it calls the failure.
func TestAppliedUnknownWinsOverTheReason(t *testing.T) {
	h := newHarness(t, respondWith(http.StatusInternalServerError,
		hooks.WorkspaceFailure(hooks.WorkspaceOpAdd, hooks.ReasonOperationFailed, hooks.AppliedUnknown,
			"the clone happened but recording it did not")))

	code := h.run(t, Request{Op: hooks.WorkspaceOpAdd, Params: Params{Repo: "beta"}})

	wantExit(t, code, ExitUnknown, h)
}

func TestUnavailableExitsUnavailableAndSaysRetryingIsSafe(t *testing.T) {
	h := newHarness(t, respondWith(http.StatusServiceUnavailable,
		hooks.WorkspaceFailure(hooks.WorkspaceOpAdd, hooks.ReasonNotAccepted, hooks.AppliedNo,
			"krang did not accept the request within 60s")))

	code := h.run(t, Request{Op: hooks.WorkspaceOpAdd, Params: Params{Repo: "beta"}})

	wantExit(t, code, ExitUnavailable, h)
	if !strings.Contains(h.stderr.String(), "Nothing was applied") {
		t.Errorf("stderr = %q, want the guarantee that nothing ran", h.stderr.String())
	}
}

func TestUnknownTaskExitsGenericError(t *testing.T) {
	h := newHarness(t, respondWith(http.StatusNotFound,
		hooks.WorkspaceFailure(hooks.WorkspaceOpList, hooks.ReasonUnknownTask, hooks.AppliedNo,
			`no live task owns a workspace containing "/tmp/elsewhere"`)))

	code := h.run(t, Request{Op: hooks.WorkspaceOpList})

	wantExit(t, code, ExitError, h)
	if !strings.Contains(h.stderr.String(), "no live task") {
		t.Errorf("stderr = %q, want krang's message surfaced", h.stderr.String())
	}
}

// Every reason the server can produce has a considered exit code, so a
// new one can't quietly inherit the wrong default.
func TestExitCodeForCoversEveryServerReason(t *testing.T) {
	cases := map[string]int{
		hooks.ReasonInvalidRequest:       ExitError,
		hooks.ReasonNoWorkspace:          ExitError,
		hooks.ReasonUnknownRepo:          ExitError,
		hooks.ReasonUnknownTask:          ExitError,
		hooks.ReasonUnknownSlot:          ExitError,
		hooks.ReasonUnsupportedOperation: ExitError,
		hooks.ReasonOperationFailed:      ExitError,
		hooks.ReasonWorkspaceRoot:        ExitError,
		hooks.ReasonLabelRequired:        ExitRefused,
		hooks.ReasonSlotLimit:            ExitRefused,
		hooks.ReasonAmbiguousSlot:        ExitRefused,
		hooks.ReasonUnsavedWork:          ExitRefused,
		hooks.ReasonSharedWorkspace:      ExitRefused,
		hooks.ReasonSlotMissing:          ExitRefused,
		hooks.ReasonUnavailable:          ExitUnavailable,
		hooks.ReasonNotAccepted:          ExitUnavailable,
		hooks.ReasonExpired:              ExitUnavailable,
		hooks.ReasonTimeout:              ExitUnknown,
	}

	for reason, want := range cases {
		applied := hooks.AppliedNo
		if reason == hooks.ReasonTimeout {
			applied = hooks.AppliedUnknown
		}
		got := ExitCodeFor(hooks.WorkspaceFailure(hooks.WorkspaceOpAdd, reason, applied, ""))
		if got != want {
			t.Errorf("reason %q exits %d, want %d", reason, got, want)
		}
	}

	if got := ExitCodeFor(hooks.WorkspaceOK(hooks.WorkspaceOpList, nil)); got != ExitOK {
		t.Errorf("a successful envelope exits %d, want 0", got)
	}
}

// --- finding the instance ---

// AC: the "you are not in a krang task" case has to say so, because it
// is what an agent hits when it runs the command in the wrong place.
func TestMissingStateFileEnvExplainsItIsNotInsideATask(t *testing.T) {
	h := &harness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	h.runner = Runner{
		Getenv: func(string) string { return "" },
		Getwd:  func() (string, error) { return testCwd, nil },
		Stdout: h.stdout, Stderr: h.stderr,
	}

	code := h.run(t, Request{Op: hooks.WorkspaceOpList})

	wantExit(t, code, ExitError, h)
	stderr := h.stderr.String()
	for _, want := range []string{StateFileEnv, "task window"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
}

func TestUnreadableStateFileSaysTheInstanceProbablyExited(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone.json")
	h := newHarnessWithStateFile(t, missing)

	code := h.run(t, Request{Op: hooks.WorkspaceOpList})

	wantExit(t, code, ExitError, h)
	if !strings.Contains(h.stderr.String(), "exited") {
		t.Errorf("stderr = %q, want it to explain the state file is stale", h.stderr.String())
	}
}

func TestStateFileWithoutAPortIsRejected(t *testing.T) {
	h := newHarnessWithStateFile(t, writeStateFile(t, `{}`))

	code := h.run(t, Request{Op: hooks.WorkspaceOpList})

	wantExit(t, code, ExitError, h)
	if !strings.Contains(h.stderr.String(), "no port") {
		t.Errorf("stderr = %q, want it to say the state file names no port", h.stderr.String())
	}
}

func TestMalformedStateFileIsRejected(t *testing.T) {
	h := newHarnessWithStateFile(t, writeStateFile(t, `not json`))

	code := h.run(t, Request{Op: hooks.WorkspaceOpList})

	wantExit(t, code, ExitError, h)
	if !strings.Contains(h.stderr.String(), "valid JSON") {
		t.Errorf("stderr = %q, want it to say the state file is unparseable", h.stderr.String())
	}
}

// AC: a state file pointing at a port nobody is listening on is the
// "krang died" case, and nothing was applied, so retrying is safe.
func TestDeadPortSaysTheTUIIsNotRunning(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	h := newHarnessWithStateFile(t, writeStateFile(t, fmt.Sprintf(`{"port":%d}`, port)))

	code := h.run(t, Request{Op: hooks.WorkspaceOpList})

	wantExit(t, code, ExitUnavailable, h)
	stderr := h.stderr.String()
	if !strings.Contains(stderr, "krang TUI not running") {
		t.Errorf("stderr = %q, want the not-running diagnosis", stderr)
	}
	if !strings.Contains(stderr, fmt.Sprint(port)) {
		t.Errorf("stderr = %q, want the port it tried", stderr)
	}
}

// A krang that has been running since before the workspace API existed
// answers 404 from its router. Found by pointing the CLI at a real
// instance, which was exactly that. Routing rejects a request before any
// handler runs, so this must not read as "might have applied".
func TestEndpointMissingFromAnOlderKrangIsNotAnUnknown(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, &http.Request{})
	})

	code := h.run(t, Request{Op: hooks.WorkspaceOpAdd, Params: Params{Repo: "beta"}})

	wantExit(t, code, ExitError, h)
	stderr := h.stderr.String()
	if !strings.Contains(stderr, "does not serve POST /api/workspace/add") {
		t.Errorf("stderr = %q, want the missing endpoint named", stderr)
	}
	if !strings.Contains(stderr, "predates the workspace API") {
		t.Errorf("stderr = %q, want the reason and the fix", stderr)
	}
	if strings.Contains(stderr, "DO NOT") {
		t.Errorf("stderr = %q, must not warn about a mutation that provably never ran", stderr)
	}
}

// JSON that isn't an envelope is still not an envelope. Decoding into
// the struct succeeds and leaves everything zero, which would otherwise
// read as a nameless failure.
func TestJSONWithoutAStatusIsNotTreatedAsAnEnvelope(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	})

	code := h.run(t, Request{Op: hooks.WorkspaceOpList})

	wantExit(t, code, ExitUnknown, h)
	if !strings.Contains(h.stderr.String(), "not a workspace envelope") {
		t.Errorf("stderr = %q, want it to say the answer wasn't krang's", h.stderr.String())
	}
}

// A stale port can be reused by something else entirely. Whatever that
// is, it is not krang, and we cannot claim nothing happened.
func TestNonEnvelopeResponseIsTreatedAsUnknown(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>nginx</html>"))
	})

	code := h.run(t, Request{Op: hooks.WorkspaceOpAdd, Params: Params{Repo: "beta"}})

	wantExit(t, code, ExitUnknown, h)
	if !strings.Contains(h.stderr.String(), "not a workspace envelope") {
		t.Errorf("stderr = %q, want it to say the answer wasn't krang's", h.stderr.String())
	}
}

// The server's timeout produces an envelope that says whether the work
// may still land; a client that gave up first would replace that with a
// dead socket and no information at all.
func TestClientWaitsLongerThanTheServer(t *testing.T) {
	if DefaultTimeout <= hooks.DefaultWorkspaceTimeout {
		t.Errorf("client timeout %s must exceed the server's %s so the server is the one that times out",
			DefaultTimeout, hooks.DefaultWorkspaceTimeout)
	}
}
