package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Workspace mutations requested from outside the TUI process all funnel
// through this file. An HTTP handler decodes its parameters, builds a
// WorkspaceRequest, and hands it to submitWorkspaceRequest, which puts
// it on the channel the Bubble Tea model consumes. The model does the
// work as a tea.Cmd and answers on the request's Reply channel. Nothing
// here touches a workspace directly — serialization only works because
// the TUI process is the single writer.

// DefaultWorkspaceTimeout bounds how long an HTTP caller waits for the
// TUI to answer. It has to cover a real clone, not just the queue hop.
const DefaultWorkspaceTimeout = 60 * time.Second

// WorkspaceOp names a mutation the TUI knows how to perform.
type WorkspaceOp string

const (
	// WorkspaceOpPing is scaffolding. It exercises the whole path —
	// HTTP handler → request queue → tea.Cmd → reply → HTTP response
	// — without touching a workspace, so the mechanism is testable
	// before the real endpoints exist. Delete it or repurpose it as a
	// liveness probe once they land.
	WorkspaceOpPing WorkspaceOp = "ping"
)

// Response statuses.
const (
	WorkspaceStatusOK    = "ok"
	WorkspaceStatusError = "error"
)

// Machine-readable failure reasons. Callers branch on these; the
// Message field is for humans and may change wording freely.
const (
	// ReasonInvalidRequest — the request never made it past decoding.
	ReasonInvalidRequest = "invalid_request"
	// ReasonUnavailable — this krang build/instance has no TUI
	// consuming workspace requests.
	ReasonUnavailable = "unavailable"
	// ReasonNotAccepted — the TUI did not take the request off the
	// queue before the deadline. Nothing was applied.
	ReasonNotAccepted = "not_accepted"
	// ReasonExpired — the request sat queued past its deadline and
	// was dropped without running. Nothing was applied.
	ReasonExpired = "expired"
	// ReasonTimeout — the TUI accepted the request but did not answer
	// before the deadline. The work may still be running.
	ReasonTimeout = "timeout"
	// ReasonUnknownTask — no live task by that name.
	ReasonUnknownTask = "unknown_task"
	// ReasonUnsupportedOperation — the TUI doesn't implement the op.
	ReasonUnsupportedOperation = "unsupported_operation"
	// ReasonOperationFailed — the mutation ran and failed.
	ReasonOperationFailed = "operation_failed"
)

// Applied tells the caller whether the mutation may have taken effect.
// It is the field to branch on before retrying.
const (
	AppliedNo      = "no"
	AppliedYes     = "yes"
	AppliedUnknown = "unknown"
)

// WorkspaceRequest is one workspace mutation asked for from outside the
// Bubble Tea loop. Requests are delivered over a channel the model
// consumes the same way it consumes hook events, and executed one at a
// time.
//
// New operations add their parameters as fields here rather than a
// generic map, so the compiler keeps the HTTP handlers and the TUI
// executor in agreement.
type WorkspaceRequest struct {
	Op WorkspaceOp

	// TaskName identifies the task whose workspace changes. Callers
	// use names because that is what a Claude session, the CLI, and
	// the user all have; the TUI resolves the ID.
	TaskName string

	// Repo and Label are the slot parameters the real endpoints will
	// fill in (add-repo, create-slot). Unused by ping.
	Repo  string
	Label string

	// Message is the ping payload. Scaffolding — remove with the ping
	// operation.
	Message string

	// Deadline is when the HTTP caller stops waiting. A request still
	// sitting in the queue at its deadline is dropped rather than
	// started, so an abandoned caller can never be surprised by work
	// that begins after it gave up. Zero means no deadline.
	Deadline time.Time

	// Reply carries exactly one response. Buffered with capacity 1 by
	// NewWorkspaceRequest so the TUI's send never blocks, even when
	// the HTTP caller has already timed out and stopped reading.
	Reply chan WorkspaceResponse
}

// NewWorkspaceRequest builds a request with its reply channel wired up.
func NewWorkspaceRequest(op WorkspaceOp, taskName string) WorkspaceRequest {
	return WorkspaceRequest{
		Op:       op,
		TaskName: taskName,
		Reply:    make(chan WorkspaceResponse, 1),
	}
}

// Expired reports whether the caller's deadline has already passed.
func (r WorkspaceRequest) Expired(now time.Time) bool {
	return !r.Deadline.IsZero() && now.After(r.Deadline)
}

// WorkspaceResponse is both the TUI's answer and the JSON body of the
// HTTP response, so the shape callers parse is the shape the model
// produces.
type WorkspaceResponse struct {
	Status  string            `json:"status"`
	Op      WorkspaceOp       `json:"op,omitempty"`
	Reason  string            `json:"reason,omitempty"`
	Applied string            `json:"applied,omitempty"`
	Message string            `json:"message,omitempty"`
	Data    map[string]string `json:"data,omitempty"`
}

// WorkspaceOK builds a success response.
func WorkspaceOK(op WorkspaceOp, data map[string]string) WorkspaceResponse {
	return WorkspaceResponse{
		Status:  WorkspaceStatusOK,
		Op:      op,
		Applied: AppliedYes,
		Data:    data,
	}
}

// WorkspaceFailure builds a failure response. applied says whether the
// mutation may have taken effect despite the failure.
func WorkspaceFailure(op WorkspaceOp, reason, applied, message string) WorkspaceResponse {
	return WorkspaceResponse{
		Status:  WorkspaceStatusError,
		Op:      op,
		Reason:  reason,
		Applied: applied,
		Message: message,
	}
}

// workspaceHTTPStatus maps a failure reason to an HTTP status code.
// Anything the caller could fix is 4xx; anything about krang's own
// availability is 503 so retries are obviously appropriate.
func workspaceHTTPStatus(resp WorkspaceResponse) int {
	if resp.Status == WorkspaceStatusOK {
		return http.StatusOK
	}
	switch resp.Reason {
	case ReasonInvalidRequest, ReasonUnsupportedOperation:
		return http.StatusBadRequest
	case ReasonUnknownTask:
		return http.StatusNotFound
	case ReasonUnavailable, ReasonNotAccepted, ReasonExpired, ReasonTimeout:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func writeWorkspaceResponse(w http.ResponseWriter, resp WorkspaceResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(workspaceHTTPStatus(resp))
	_ = json.NewEncoder(w).Encode(resp)
}

// submitWorkspaceRequest hands req to the TUI, waits a bounded time for
// the reply, and writes the HTTP response. Every /api/workspace/*
// endpoint should be a thin parameter decoder in front of this call.
//
// Timeout semantics, which callers must understand:
//
//   - Before the TUI accepts the request, a timeout means the request
//     never ran (reason "not_accepted", applied "no"). Same for a
//     request dropped from the queue at its deadline (reason
//     "expired").
//   - Once accepted, abandoning the wait does NOT cancel the work. The
//     TUI owns the request and will run it to completion, record its
//     provenance, write the events-table row, and log it — the reply
//     just lands in a channel nobody reads. That is why the timeout
//     response says applied "unknown": the caller must re-read state
//     rather than assume nothing happened. Half-applied state is
//     impossible because the operation itself is what's atomic, not
//     the HTTP wait.
func (s *Server) submitWorkspaceRequest(w http.ResponseWriter, r *http.Request, req WorkspaceRequest) {
	if s.workspaceRequests == nil {
		writeWorkspaceResponse(w, WorkspaceFailure(req.Op, ReasonUnavailable, AppliedNo,
			"this krang instance is not accepting workspace requests"))
		return
	}
	if req.Reply == nil {
		req.Reply = make(chan WorkspaceResponse, 1)
	}

	timeout := s.WorkspaceTimeout
	if timeout <= 0 {
		timeout = DefaultWorkspaceTimeout
	}
	req.Deadline = time.Now().Add(timeout)

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	select {
	case s.workspaceRequests <- req:
	case <-r.Context().Done():
		return
	case <-deadline.C:
		writeWorkspaceResponse(w, WorkspaceFailure(req.Op, ReasonNotAccepted, AppliedNo,
			fmt.Sprintf("krang did not accept the request within %s", timeout)))
		return
	}

	select {
	case resp := <-req.Reply:
		if resp.Op == "" {
			resp.Op = req.Op
		}
		writeWorkspaceResponse(w, resp)
	case <-r.Context().Done():
		return
	case <-deadline.C:
		writeWorkspaceResponse(w, WorkspaceFailure(req.Op, ReasonTimeout, AppliedUnknown,
			fmt.Sprintf("krang did not finish %s within %s; the operation may still complete, so re-read the workspace before retrying", req.Op, timeout)))
	}
}

// handleWorkspacePing is scaffolding for the workspace request
// mechanism: it resolves the task and echoes a message back, proving
// the plumbing end to end without mutating anything. It is registered
// as POST /api/workspace/ping and should be removed or repurposed once
// the real endpoints exist.
func (s *Server) handleWorkspacePing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Task    string `json:"task"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeWorkspaceResponse(w, WorkspaceFailure(WorkspaceOpPing, ReasonInvalidRequest, AppliedNo,
			"body must be a JSON object"))
		return
	}
	if body.Task == "" {
		writeWorkspaceResponse(w, WorkspaceFailure(WorkspaceOpPing, ReasonInvalidRequest, AppliedNo,
			`"task" is required`))
		return
	}

	req := NewWorkspaceRequest(WorkspaceOpPing, body.Task)
	req.Message = body.Message
	s.submitWorkspaceRequest(w, r, req)
}
