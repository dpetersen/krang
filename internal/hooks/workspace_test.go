package hooks

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func postPing(t *testing.T, ts *httptest.Server, body string) (*http.Response, WorkspaceResponse) {
	t.Helper()

	resp, err := ts.Client().Post(ts.URL+"/api/workspace/ping", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("posting ping: %v", err)
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

func TestWorkspacePingRoundTrip(t *testing.T) {
	reqs := make(chan WorkspaceRequest, 1)
	ts := newWorkspaceTestServer(t, reqs, 2*time.Second)

	go func() {
		req := <-reqs
		req.Reply <- WorkspaceOK(req.Op, map[string]string{"echo": req.Message, "task": req.TaskName})
	}()

	httpResp, resp := postPing(t, ts, `{"task":"alpha","message":"hi"}`)

	if httpResp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", httpResp.StatusCode)
	}
	if resp.Status != WorkspaceStatusOK {
		t.Errorf("status = %q, want %q", resp.Status, WorkspaceStatusOK)
	}
	if resp.Op != WorkspaceOpPing {
		t.Errorf("op = %q, want %q", resp.Op, WorkspaceOpPing)
	}
	if resp.Data["echo"] != "hi" || resp.Data["task"] != "alpha" {
		t.Errorf("data = %v, want the echoed message and task", resp.Data)
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

	httpResp, resp := postPing(t, ts, `{"task":"alpha"}`)

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
	if resp.Op != WorkspaceOpPing {
		t.Errorf("op = %q, want %q", resp.Op, WorkspaceOpPing)
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

	httpResp, resp := postPing(t, ts, `{"task":"alpha"}`)

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

	httpResp, resp := postPing(t, ts, `{"task":"alpha"}`)

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

func TestWorkspacePingRejectsMissingTask(t *testing.T) {
	reqs := make(chan WorkspaceRequest, 1)
	ts := newWorkspaceTestServer(t, reqs, time.Second)

	httpResp, resp := postPing(t, ts, `{"message":"hi"}`)

	if httpResp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", httpResp.StatusCode)
	}
	if resp.Reason != ReasonInvalidRequest {
		t.Errorf("reason = %q, want %q", resp.Reason, ReasonInvalidRequest)
	}
	if len(reqs) != 0 {
		t.Error("an undecodable request was still handed to the TUI")
	}
}

func TestWorkspacePingRejectsMalformedBody(t *testing.T) {
	ts := newWorkspaceTestServer(t, make(chan WorkspaceRequest, 1), time.Second)

	httpResp, resp := postPing(t, ts, `not json`)

	if httpResp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", httpResp.StatusCode)
	}
	if resp.Reason != ReasonInvalidRequest {
		t.Errorf("reason = %q, want %q", resp.Reason, ReasonInvalidRequest)
	}
}

// The TUI's own failures come back through the same envelope, mapped to
// a status code the caller can act on.
func TestWorkspaceRequestUnknownTaskReturns404(t *testing.T) {
	reqs := make(chan WorkspaceRequest, 1)
	ts := newWorkspaceTestServer(t, reqs, 2*time.Second)

	go func() {
		req := <-reqs
		req.Reply <- WorkspaceFailure(req.Op, ReasonUnknownTask, AppliedNo, "no live task named \"ghost\"")
	}()

	httpResp, resp := postPing(t, ts, `{"task":"ghost"}`)

	if httpResp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", httpResp.StatusCode)
	}
	if resp.Reason != ReasonUnknownTask {
		t.Errorf("reason = %q, want %q", resp.Reason, ReasonUnknownTask)
	}
}
