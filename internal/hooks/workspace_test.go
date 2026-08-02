package hooks

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// newWorkspaceTestServer builds a hook server wired to reqs and returns
// an httptest server for its mux. Nothing is started or written to
// disk — only the HTTP handlers are exercised.
func newWorkspaceTestServer(t *testing.T, reqs chan WorkspaceRequest, timeout time.Duration) *httptest.Server {
	t.Helper()

	var ch chan<- WorkspaceRequest
	if reqs != nil {
		ch = reqs
	}
	s := NewServer(t.TempDir()+"/state.json", nil, ch)
	s.WorkspaceTimeout = timeout

	ts := httptest.NewServer(s.httpServer.Handler)
	t.Cleanup(ts.Close)
	return ts
}

// callWorkspace issues a request against a workspace endpoint and
// decodes the envelope. Every endpoint answers with the same shape, so
// every handler test goes through here.
func callWorkspace(t *testing.T, ts *httptest.Server, method, path, body string) (*http.Response, WorkspaceResponse) {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var decoded WorkspaceResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return resp, decoded
}

func postAdd(t *testing.T, ts *httptest.Server, body string) (*http.Response, WorkspaceResponse) {
	t.Helper()
	return callWorkspace(t, ts, http.MethodPost, "/api/workspace/add", body)
}

// --- the request plumbing, exercised through a real mutating op ---

func TestWorkspaceAddRoundTrip(t *testing.T) {
	reqs := make(chan WorkspaceRequest, 1)
	ts := newWorkspaceTestServer(t, reqs, 2*time.Second)

	var seen WorkspaceRequest
	accepted := make(chan struct{})
	go func() {
		seen = <-reqs
		close(accepted)
		slot := SlotInfo{Dir: "beta", Repo: "beta", VCS: "jj", VCSName: "alpha", Base: "main@origin", Exists: true, Recorded: true}
		resp := WorkspaceOK(seen.Op, map[string]string{"workspace_dir": "/ws/alpha"})
		resp.Task = "alpha"
		resp.Slot = &slot
		seen.Reply <- resp
	}()

	httpResp, resp := postAdd(t, ts, `{"task":"alpha","repo":"beta","base":"main@origin"}`)
	<-accepted

	if httpResp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", httpResp.StatusCode)
	}
	if resp.Status != WorkspaceStatusOK || resp.Op != WorkspaceOpAdd {
		t.Errorf("envelope = %+v, want an ok add", resp)
	}
	if resp.Slot == nil || resp.Slot.Dir != "beta" {
		t.Errorf("slot = %+v, want the created working copy", resp.Slot)
	}
	if resp.Task != "alpha" {
		t.Errorf("task = %q, want the resolved task echoed back", resp.Task)
	}
	if seen.Repo != "beta" || seen.Base != "main@origin" {
		t.Errorf("delivered request = %+v, want repo and base plumbed through", seen)
	}
}

// The TUI takes the request but never answers. The caller must get a
// machine-readable 503 that admits the work may still land, because
// abandoning the wait does not cancel it.
func TestWorkspaceRequestTimeoutReturns503WithReason(t *testing.T) {
	reqs := make(chan WorkspaceRequest, 1)
	ts := newWorkspaceTestServer(t, reqs, 50*time.Millisecond)

	accepted := make(chan WorkspaceRequest, 1)
	go func() { accepted <- <-reqs }()

	httpResp, resp := postAdd(t, ts, `{"task":"alpha","repo":"beta"}`)

	if httpResp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", httpResp.StatusCode)
	}
	if resp.Status != WorkspaceStatusError {
		t.Errorf("status = %q, want %q", resp.Status, WorkspaceStatusError)
	}
	if resp.Reason != ReasonTimeout {
		t.Errorf("reason = %q, want %q", resp.Reason, ReasonTimeout)
	}
	if resp.Applied != AppliedUnknown {
		t.Errorf("applied = %q, want %q — an accepted request may still complete", resp.Applied, AppliedUnknown)
	}
	if resp.Op != WorkspaceOpAdd {
		t.Errorf("op = %q, want %q", resp.Op, WorkspaceOpAdd)
	}
	if resp.Message == "" {
		t.Error("message is empty; the human-readable half of the failure is missing")
	}

	select {
	case req := <-accepted:
		if req.Deadline.IsZero() {
			t.Error("delivered request has no deadline; the TUI can't drop abandoned queue entries")
		}
	case <-time.After(time.Second):
		t.Fatal("request was never delivered to the TUI")
	}
}

// Nothing is reading the channel, so the request is never accepted and
// the caller gets a hard guarantee that nothing was applied.
func TestWorkspaceRequestNotAcceptedReports503AppliedNo(t *testing.T) {
	ts := newWorkspaceTestServer(t, make(chan WorkspaceRequest), 50*time.Millisecond)

	httpResp, resp := postAdd(t, ts, `{"task":"alpha","repo":"beta"}`)

	if httpResp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", httpResp.StatusCode)
	}
	if resp.Reason != ReasonNotAccepted {
		t.Errorf("reason = %q, want %q", resp.Reason, ReasonNotAccepted)
	}
	if resp.Applied != AppliedNo {
		t.Errorf("applied = %q, want %q — an unaccepted request cannot have run", resp.Applied, AppliedNo)
	}
}

func TestWorkspaceRequestUnavailableWithoutChannel(t *testing.T) {
	ts := newWorkspaceTestServer(t, nil, time.Second)

	httpResp, resp := postAdd(t, ts, `{"task":"alpha","repo":"beta"}`)

	if httpResp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", httpResp.StatusCode)
	}
	if resp.Reason != ReasonUnavailable {
		t.Errorf("reason = %q, want %q", resp.Reason, ReasonUnavailable)
	}
	if resp.Applied != AppliedNo {
		t.Errorf("applied = %q, want %q", resp.Applied, AppliedNo)
	}
}

// The TUI's own failures come back through the same envelope, mapped to
// a status code the caller can act on.
func TestWorkspaceRequestUnknownTaskReturns404(t *testing.T) {
	reqs := make(chan WorkspaceRequest, 1)
	ts := newWorkspaceTestServer(t, reqs, 2*time.Second)

	go func() {
		req := <-reqs
		req.Reply <- WorkspaceFailure(req.Op, ReasonUnknownTask, AppliedNo, `no live task named "ghost"`)
	}()

	httpResp, resp := postAdd(t, ts, `{"task":"ghost","repo":"beta"}`)

	if httpResp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", httpResp.StatusCode)
	}
	if resp.Reason != ReasonUnknownTask {
		t.Errorf("reason = %q, want %q", resp.Reason, ReasonUnknownTask)
	}
}

// --- decoders ---

// Every endpoint refuses a request that doesn't say which task it means,
// and refuses it before the TUI ever hears about it.
func TestWorkspaceEndpointsRequireATaskOrCwd(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   string
		op     WorkspaceOp
	}{
		{"list", http.MethodGet, "/api/workspace", "", WorkspaceOpList},
		{"repos", http.MethodGet, "/api/workspace/repos", "", WorkspaceOpRepos},
		{"add", http.MethodPost, "/api/workspace/add", `{"repo":"beta"}`, WorkspaceOpAdd},
		{"remove", http.MethodDelete, "/api/workspace/slot", `{"dir":"beta"}`, WorkspaceOpRemoveSlot},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqs := make(chan WorkspaceRequest, 1)
			ts := newWorkspaceTestServer(t, reqs, time.Second)

			httpResp, resp := callWorkspace(t, ts, tc.method, tc.path, tc.body)

			if httpResp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", httpResp.StatusCode)
			}
			if resp.Reason != ReasonInvalidRequest {
				t.Errorf("reason = %q, want %q", resp.Reason, ReasonInvalidRequest)
			}
			if resp.Op != tc.op {
				t.Errorf("op = %q, want %q", resp.Op, tc.op)
			}
			if len(reqs) != 0 {
				t.Error("an undecodable request was still handed to the TUI")
			}
		})
	}
}

// A cwd is enough on its own: an agent inside a workspace knows where
// it is without knowing what krang calls the task.
func TestWorkspaceEndpointsAcceptCwdInsteadOfTask(t *testing.T) {
	reqs := make(chan WorkspaceRequest, 1)
	ts := newWorkspaceTestServer(t, reqs, time.Second)

	go func() {
		req := <-reqs
		resp := WorkspaceOK(req.Op, nil)
		resp.Task = "alpha"
		req.Reply <- resp
	}()

	_, resp := callWorkspace(t, ts, http.MethodGet, "/api/workspace?cwd=/ws/alpha/beta", "")

	if resp.Status != WorkspaceStatusOK {
		t.Fatalf("envelope = %+v, want ok", resp)
	}
	if resp.Task != "alpha" {
		t.Errorf("task = %q, want the cwd-resolved task", resp.Task)
	}
}

func TestWorkspaceAddRejectsMissingRepo(t *testing.T) {
	reqs := make(chan WorkspaceRequest, 1)
	ts := newWorkspaceTestServer(t, reqs, time.Second)

	httpResp, resp := postAdd(t, ts, `{"task":"alpha"}`)

	if httpResp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", httpResp.StatusCode)
	}
	if resp.Reason != ReasonInvalidRequest {
		t.Errorf("reason = %q, want %q", resp.Reason, ReasonInvalidRequest)
	}
	if len(reqs) != 0 {
		t.Error("a request with no repo was handed to the TUI")
	}
}

func TestWorkspaceRemoveSlotRejectsUnnamedSlot(t *testing.T) {
	reqs := make(chan WorkspaceRequest, 1)
	ts := newWorkspaceTestServer(t, reqs, time.Second)

	httpResp, resp := callWorkspace(t, ts, http.MethodDelete, "/api/workspace/slot", `{"task":"alpha"}`)

	if httpResp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", httpResp.StatusCode)
	}
	if resp.Reason != ReasonInvalidRequest {
		t.Errorf("reason = %q, want %q", resp.Reason, ReasonInvalidRequest)
	}
	if len(reqs) != 0 {
		t.Error("a request naming no slot was handed to the TUI")
	}
}

func TestWorkspaceRemoveSlotPlumbsForceAndSlotName(t *testing.T) {
	reqs := make(chan WorkspaceRequest, 1)
	ts := newWorkspaceTestServer(t, reqs, time.Second)

	var seen WorkspaceRequest
	done := make(chan struct{})
	go func() {
		seen = <-reqs
		close(done)
		seen.Reply <- WorkspaceOK(seen.Op, nil)
	}()

	callWorkspace(t, ts, http.MethodDelete, "/api/workspace/slot",
		`{"task":"alpha","dir":"beta--tests","force":true}`)
	<-done

	if seen.Dir != "beta--tests" || !seen.Force {
		t.Errorf("delivered request = %+v, want the slot dir and force flag", seen)
	}
}

func TestWorkspaceAddRejectsMalformedBody(t *testing.T) {
	ts := newWorkspaceTestServer(t, make(chan WorkspaceRequest, 1), time.Second)

	httpResp, resp := postAdd(t, ts, `not json`)

	if httpResp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", httpResp.StatusCode)
	}
	if resp.Reason != ReasonInvalidRequest {
		t.Errorf("reason = %q, want %q", resp.Reason, ReasonInvalidRequest)
	}
}

// --- the wire contract ---

// AC: the listing publishes exactly these keys per slot. Callers that
// branch on `exists` or `recorded` need the key to be there even when
// the value is false, so nothing here may be omitempty.
func TestWorkspaceListResponseHasExactSlotKeys(t *testing.T) {
	reqs := make(chan WorkspaceRequest, 1)
	ts := newWorkspaceTestServer(t, reqs, 2*time.Second)

	go func() {
		req := <-reqs
		resp := WorkspaceOK(req.Op, nil)
		// Deliberately a zero-valued slot: the empty strings and false
		// booleans are exactly the values omitempty would eat.
		resp.Slots = []SlotInfo{{}}
		req.Reply <- resp
	}()

	httpResp, err := ts.Client().Get(ts.URL + "/api/workspace?task=alpha")
	if err != nil {
		t.Fatalf("GET /api/workspace: %v", err)
	}
	defer httpResp.Body.Close()

	var body struct {
		Slots []map[string]any `json:"slots"`
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(body.Slots) != 1 {
		t.Fatalf("got %d slots, want 1", len(body.Slots))
	}

	want := []string{"base", "canonical_repo_path", "dir", "exists", "recorded", "repo", "slot", "vcs", "vcs_name"}
	var got []string
	for key := range body.Slots[0] {
		got = append(got, key)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("slot keys = %v, want exactly %v", got, want)
	}
}

func TestWorkspaceReposResponseHasExactRepoKeys(t *testing.T) {
	reqs := make(chan WorkspaceRequest, 1)
	ts := newWorkspaceTestServer(t, reqs, 2*time.Second)

	go func() {
		req := <-reqs
		resp := WorkspaceOK(req.Op, nil)
		resp.Repos = []RepoInfo{{}}
		req.Reply <- resp
	}()

	httpResp, err := ts.Client().Get(ts.URL + "/api/workspace/repos?task=alpha")
	if err != nil {
		t.Fatalf("GET /api/workspace/repos: %v", err)
	}
	defer httpResp.Body.Close()

	var body struct {
		Repos []map[string]any `json:"repos"`
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(body.Repos) != 1 {
		t.Fatalf("got %d repos, want 1", len(body.Repos))
	}

	want := []string{"in_task", "name", "sets"}
	var got []string
	for key := range body.Repos[0] {
		got = append(got, key)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("repo keys = %v, want exactly %v", got, want)
	}
}

// AC: refusing to destroy work is a 409 the caller can act on, carrying
// a machine-readable list of what it is protecting.
func TestUnsavedWorkRefusalIs409WithBlockers(t *testing.T) {
	reqs := make(chan WorkspaceRequest, 1)
	ts := newWorkspaceTestServer(t, reqs, 2*time.Second)

	go func() {
		req := <-reqs
		resp := WorkspaceFailure(req.Op, ReasonUnsavedWork, AppliedNo, "removing \"beta\" would lose work")
		resp.Blockers = []RemovalBlocker{{Dir: "beta", Kind: BlockerUnpushedCommits, Detail: "2 commits"}}
		req.Reply <- resp
	}()

	httpResp, resp := callWorkspace(t, ts, http.MethodDelete, "/api/workspace/slot",
		`{"task":"alpha","dir":"beta"}`)

	if httpResp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", httpResp.StatusCode)
	}
	if resp.Applied != AppliedNo {
		t.Errorf("applied = %q, want %q — a refused removal changed nothing", resp.Applied, AppliedNo)
	}
	if len(resp.Blockers) != 1 || resp.Blockers[0].Kind != BlockerUnpushedCommits {
		t.Errorf("blockers = %+v, want the unpushed-commits blocker", resp.Blockers)
	}
}

func TestWorkspaceStatusCodesCoverEveryReason(t *testing.T) {
	cases := map[string]int{
		ReasonInvalidRequest:       http.StatusBadRequest,
		ReasonNoWorkspace:          http.StatusBadRequest,
		ReasonUnknownRepo:          http.StatusBadRequest,
		ReasonLabelRequired:        http.StatusBadRequest,
		ReasonSlotLimit:            http.StatusBadRequest,
		ReasonAmbiguousSlot:        http.StatusBadRequest,
		ReasonUnsupportedOperation: http.StatusBadRequest,
		ReasonUnknownTask:          http.StatusNotFound,
		ReasonUnknownSlot:          http.StatusNotFound,
		ReasonUnsavedWork:          http.StatusConflict,
		ReasonSharedWorkspace:      http.StatusConflict,
		ReasonSlotMissing:          http.StatusConflict,
		ReasonUnavailable:          http.StatusServiceUnavailable,
		ReasonNotAccepted:          http.StatusServiceUnavailable,
		ReasonExpired:              http.StatusServiceUnavailable,
		ReasonTimeout:              http.StatusServiceUnavailable,
		ReasonOperationFailed:      http.StatusInternalServerError,
	}

	for reason, want := range cases {
		got := workspaceHTTPStatus(WorkspaceFailure(WorkspaceOpAdd, reason, AppliedNo, ""))
		if got != want {
			t.Errorf("reason %q maps to %d, want %d", reason, got, want)
		}
	}
}

// Reads and mutations are classified once, in the type, so the TUI's
// queue and the HTTP layer can't disagree about which is which.
func TestReadOnlyOpsAreListAndRepos(t *testing.T) {
	for _, op := range []WorkspaceOp{WorkspaceOpList, WorkspaceOpRepos} {
		if !op.ReadOnly() {
			t.Errorf("%q should be read-only", op)
		}
	}
	for _, op := range []WorkspaceOp{WorkspaceOpAdd, WorkspaceOpRemoveSlot} {
		if op.ReadOnly() {
			t.Errorf("%q must not be read-only: it mutates the workspace", op)
		}
	}
}
