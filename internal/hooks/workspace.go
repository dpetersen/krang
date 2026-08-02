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

// WorkspaceOp names an operation the TUI knows how to perform.
type WorkspaceOp string

const (
	// WorkspaceOpList enumerates the working copies in a task's
	// workspace. Read-only.
	WorkspaceOpList WorkspaceOp = "list"
	// WorkspaceOpRepos lists the repos the metarepo makes available.
	// Read-only.
	WorkspaceOpRepos WorkspaceOp = "repos"
	// WorkspaceOpAdd gives a task's workspace one more working copy —
	// of a repo it doesn't hold yet, or another slot of one it does.
	WorkspaceOpAdd WorkspaceOp = "add"
	// WorkspaceOpRemoveSlot tears one working copy back out.
	WorkspaceOpRemoveSlot WorkspaceOp = "remove_slot"
)

// ReadOnly reports whether an operation only reads workspace state.
// Read-only operations skip the mutation queue: they take no locks and
// change nothing, so making them wait behind a modal the human might
// leave open indefinitely would break them for no benefit. See
// Model.handleWorkspaceRequest for the full argument.
func (op WorkspaceOp) ReadOnly() bool {
	switch op {
	case WorkspaceOpList, WorkspaceOpRepos:
		return true
	default:
		return false
	}
}

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
	// ReasonNoWorkspace — the task exists but has no workspace
	// directory, so there is nothing to enumerate or add to.
	ReasonNoWorkspace = "no_workspace"
	// ReasonUnknownRepo — the named repo is not in the metarepo's
	// repos dir. GET /api/workspace/repos lists what is.
	ReasonUnknownRepo = "unknown_repo"
	// ReasonLabelRequired — the workspace already holds this repo, so
	// the new slot needs a label. The message suggests a free one.
	ReasonLabelRequired = "label_required"
	// ReasonSlotLimit — the task is already at MaxSlotsPerTask. The
	// message names the slots that could be removed to make room.
	ReasonSlotLimit = "slot_limit"
	// ReasonSharedWorkspace — the workspace belongs to more than one
	// task, and krang has no answer for who owns its slots.
	ReasonSharedWorkspace = "shared_workspace"
	// ReasonUnknownSlot — no slot in the workspace matches the request.
	ReasonUnknownSlot = "unknown_slot"
	// ReasonAmbiguousSlot — the request matches more than one slot.
	ReasonAmbiguousSlot = "ambiguous_slot"
	// ReasonSlotMissing — the slot is recorded but not on disk.
	ReasonSlotMissing = "slot_missing"
	// ReasonWorkspaceRoot — the slot named IS the task's workspace
	// directory (a single_repo task's initial checkout), so removing it
	// would tear down the task's whole working directory.
	ReasonWorkspaceRoot = "workspace_root"
	// ReasonUnsavedWork — removing the slot would destroy uncommitted
	// or unpushed work. Blockers says what; {"force":true} overrides.
	ReasonUnsavedWork = "unsaved_work"
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

	// TaskName identifies the task whose workspace the request is
	// about. Callers use names because that is what a Claude session,
	// the CLI, and the user all have; the TUI resolves the ID.
	TaskName string

	// Cwd is the caller's working directory, used to find the task when
	// TaskName is empty. An agent running inside a workspace knows
	// where it is without knowing what krang calls it.
	Cwd string

	// Repo names the repo under the metarepo's repos dir. Add uses it
	// to pick a source; remove accepts it (with Label) as an
	// alternative way to name a slot.
	Repo string

	// Label is the slot label. Empty means a task's initial working
	// copy of the repo.
	Label string

	// Dir names a slot by its directory inside the workspace, which is
	// the key GET /api/workspace reports and the only unambiguous one.
	Dir string

	// Base is the revset (jj) or commit-ish (git) a new slot starts
	// from. Empty means detect the remote default branch.
	Base string

	// Force waives the refusal that protects unsaved work on removal.
	Force bool

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

// SlotInfo describes one working copy inside a task's workspace. Its
// field set is the contract GET /api/workspace publishes, so every key
// is always present — a caller checking `exists` must not have to tell
// "false" from "the server didn't say".
//
// Recorded distinguishes a slot krang made and wrote a workspace_repos
// row for from one a filesystem scan merely found. The difference
// matters: only a recorded slot has a VCS identity krang knows how to
// forget, so an unrecorded one is reported rather than acted on
// confidently.
type SlotInfo struct {
	Dir               string `json:"dir"`
	Repo              string `json:"repo"`
	CanonicalRepoPath string `json:"canonical_repo_path"`
	VCS               string `json:"vcs"`
	VCSName           string `json:"vcs_name"`
	Slot              string `json:"slot"`
	Base              string `json:"base"`
	Exists            bool   `json:"exists"`
	Recorded          bool   `json:"recorded"`
}

// RepoInfo describes one repo the metarepo makes available.
type RepoInfo struct {
	Name   string   `json:"name"`
	InTask bool     `json:"in_task"`
	Sets   []string `json:"sets"`
}

// RemovalBlocker names work that removing a slot would destroy.
type RemovalBlocker struct {
	Dir    string `json:"dir"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Blocker kinds. Callers branch on these; Detail is for humans.
const (
	BlockerUncommittedChanges = "uncommitted_changes"
	BlockerUnpushedCommits    = "unpushed_commits"
)

// WorkspaceResponse is both the TUI's answer and the JSON body of the
// HTTP response, so the shape callers parse is the shape the model
// produces.
//
// The payload fields are typed and per-operation rather than a single
// generic blob, for the same reason WorkspaceRequest's parameters are:
// the compiler is then the thing keeping the handler, the executor, and
// the eventual CLI in agreement about what an operation answers with.
type WorkspaceResponse struct {
	Status  string            `json:"status"`
	Op      WorkspaceOp       `json:"op,omitempty"`
	Reason  string            `json:"reason,omitempty"`
	Applied string            `json:"applied,omitempty"`
	Message string            `json:"message,omitempty"`
	Data    map[string]string `json:"data,omitempty"`

	// Task is the task krang resolved the request to. Echoed back
	// because a caller identifying itself by cwd doesn't know it.
	Task string `json:"task,omitempty"`

	// Slots is the workspace listing (list).
	Slots []SlotInfo `json:"slots,omitempty"`
	// Repos is the registry listing (repos).
	Repos []RepoInfo `json:"repos,omitempty"`
	// Slot is the single working copy an add or remove acted on.
	Slot *SlotInfo `json:"slot,omitempty"`
	// Blockers is what an unsaved_work refusal is protecting.
	Blockers []RemovalBlocker `json:"blockers,omitempty"`
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
	case ReasonInvalidRequest, ReasonUnsupportedOperation, ReasonNoWorkspace,
		ReasonUnknownRepo, ReasonLabelRequired, ReasonSlotLimit, ReasonAmbiguousSlot:
		return http.StatusBadRequest
	case ReasonUnknownTask, ReasonUnknownSlot:
		return http.StatusNotFound
	case ReasonUnsavedWork, ReasonSharedWorkspace, ReasonSlotMissing:
		// Conflicts with the current state of the world, not with the
		// request itself. A caller that resolves the conflict — pushes,
		// forces, stops sharing — can send the very same request again.
		return http.StatusConflict
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

// workspaceRequestBody is the JSON every mutating workspace endpoint
// accepts. One struct rather than one per endpoint keeps the wire names
// from drifting apart — "task"/"cwd" mean the same thing everywhere,
// and an endpoint simply ignores the fields it has no use for.
type workspaceRequestBody struct {
	Task  string `json:"task"`
	Cwd   string `json:"cwd"`
	Repo  string `json:"repo"`
	Label string `json:"label"`
	Dir   string `json:"dir"`
	Base  string `json:"base"`
	Force bool   `json:"force"`
}

// decodeWorkspaceBody reads a JSON object body, treating an empty body
// as an empty object. Returns false after writing the failure response.
func decodeWorkspaceBody(w http.ResponseWriter, r *http.Request, op WorkspaceOp) (workspaceRequestBody, bool) {
	var body workspaceRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeWorkspaceResponse(w, WorkspaceFailure(op, ReasonInvalidRequest, AppliedNo,
			"body must be a JSON object"))
		return body, false
	}
	return body, true
}

// newTaskScopedRequest builds a request from the two ways a caller can
// say which task it means. Every endpoint goes through here so "which
// task?" has exactly one answer everywhere, and so the error for
// answering it badly is worded once.
func newTaskScopedRequest(w http.ResponseWriter, op WorkspaceOp, task, cwd string) (WorkspaceRequest, bool) {
	if task == "" && cwd == "" {
		writeWorkspaceResponse(w, WorkspaceFailure(op, ReasonInvalidRequest, AppliedNo,
			`name the task: send "task" with its name, or "cwd" with a directory inside its workspace`))
		return WorkspaceRequest{}, false
	}
	req := NewWorkspaceRequest(op, task)
	req.Cwd = cwd
	return req, true
}

// handleWorkspaceList answers GET /api/workspace with every working
// copy the task's workspace holds.
func (s *Server) handleWorkspaceList(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	req, ok := newTaskScopedRequest(w, WorkspaceOpList, query.Get("task"), query.Get("cwd"))
	if !ok {
		return
	}
	s.submitWorkspaceRequest(w, r, req)
}

// handleWorkspaceRepos answers GET /api/workspace/repos with the repos
// the metarepo makes available and which of them the task already holds.
func (s *Server) handleWorkspaceRepos(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	req, ok := newTaskScopedRequest(w, WorkspaceOpRepos, query.Get("task"), query.Get("cwd"))
	if !ok {
		return
	}
	s.submitWorkspaceRequest(w, r, req)
}

// handleWorkspaceAdd answers POST /api/workspace/add. One verb covers
// both "this task needs a repo it doesn't have" and "this task needs a
// second checkout of a repo it does" — from the caller's side those are
// the same wish, and which one it turns out to be is decided by what
// the workspace already holds, not by which URL was posted to.
func (s *Server) handleWorkspaceAdd(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeWorkspaceBody(w, r, WorkspaceOpAdd)
	if !ok {
		return
	}
	req, ok := newTaskScopedRequest(w, WorkspaceOpAdd, body.Task, body.Cwd)
	if !ok {
		return
	}
	if body.Repo == "" {
		writeWorkspaceResponse(w, WorkspaceFailure(WorkspaceOpAdd, ReasonInvalidRequest, AppliedNo,
			`"repo" is required`))
		return
	}
	req.Repo = body.Repo
	req.Label = body.Label
	req.Base = body.Base
	s.submitWorkspaceRequest(w, r, req)
}

// handleWorkspaceRemoveSlot answers DELETE /api/workspace/slot. The
// slot is named by "dir" (what the listing reports) or by "repo" plus
// optional "label".
func (s *Server) handleWorkspaceRemoveSlot(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeWorkspaceBody(w, r, WorkspaceOpRemoveSlot)
	if !ok {
		return
	}
	req, ok := newTaskScopedRequest(w, WorkspaceOpRemoveSlot, body.Task, body.Cwd)
	if !ok {
		return
	}
	if body.Dir == "" && body.Repo == "" {
		writeWorkspaceResponse(w, WorkspaceFailure(WorkspaceOpRemoveSlot, ReasonInvalidRequest, AppliedNo,
			`name the slot: send "dir" as reported by GET /api/workspace, or "repo" with an optional "label"`))
		return
	}
	req.Dir = body.Dir
	req.Repo = body.Repo
	req.Label = body.Label
	req.Force = body.Force
	s.submitWorkspaceRequest(w, r, req)
}
