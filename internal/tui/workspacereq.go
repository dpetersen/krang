package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/hooks"
)

// Workspace requests arrive from the hook HTTP server on a channel the
// model consumes exactly the way it consumes hook events: a blocking
// receive inside a tea.Cmd that re-arms itself after every delivery.
//
// The Update loop never does the work. It appends the request to a FIFO
// queue and, once nothing else is mutating a workspace, launches the
// request as a tea.Cmd. Completion comes back as
// workspaceRequestDoneMsg, which frees the in-flight slot and lets the
// next queued request start. That gives one mutation at a time across
// both sources of them — the agent over HTTP and the human at the
// keyboard — because workspaceBusy consults the same wsProgress/modal
// state the keyboard flows drive.

// workspaceRequestMsg delivers one request from the hook server.
type workspaceRequestMsg struct {
	Request hooks.WorkspaceRequest
}

// workspaceRequestDoneMsg reports a finished request. The reply has
// already been sent by the time this lands — Update only records it.
type workspaceRequestDoneMsg struct {
	Op       hooks.WorkspaceOp
	TaskName string
	Response hooks.WorkspaceResponse
	Elapsed  time.Duration

	// ReadOnly marks a request that never took the in-flight slot, so
	// completing it must not free one. Without this a listing finishing
	// mid-clone would tell Update the clone was over.
	ReadOnly bool
}

// waitForWorkspaceRequest blocks on the request channel and re-arms
// after each delivery, mirroring waitForHookEvent.
func (m Model) waitForWorkspaceRequest() tea.Cmd {
	if m.workspaceRequests == nil {
		return nil
	}
	return func() tea.Msg {
		req, ok := <-m.workspaceRequests
		if !ok {
			return nil
		}
		return workspaceRequestMsg{Request: req}
	}
}

// handleWorkspaceRequest queues an arriving mutation. It deliberately
// does no work: startNextWorkspaceRequest runs after every message and
// picks the request up as soon as the workspace is free.
//
// Read-only operations skip the queue entirely and start immediately.
// The queue exists to keep two writers off the same workspace, and a
// listing is not a writer: it reads the workspace_repos rows and stats
// some directories. Making it wait would mean waiting on `workspaceBusy`,
// which is true for as long as the human leaves a modal open — a
// listing that hangs for a minute because somebody wandered off mid-
// wizard is useless, and it would hold an HTTP connection to say so.
//
// The cost is that a listing taken while a clone is running can catch
// the workspace mid-change: a directory on disk with no row yet. That
// reads out as `recorded: false`, which is exactly what it is, so the
// answer is honest rather than merely fast. Reads still run inside the
// TUI process, so there is still only one thing reading krang's own
// view of workspace state.
func (m Model) handleWorkspaceRequest(req hooks.WorkspaceRequest) (Model, tea.Cmd) {
	if req.Op.ReadOnly() {
		return m, tea.Batch(m.runWorkspaceRequest(req), m.waitForWorkspaceRequest())
	}
	if m.workspaceBusy() {
		m.appendDebugLog(fmt.Sprintf("[%s] workspace %s task=%s queued (busy)",
			time.Now().Format("15:04:05"), req.Op, req.TaskName))
	}
	m.workspaceQueue = append(m.workspaceQueue, req)
	return m, m.waitForWorkspaceRequest()
}

// workspaceBusy reports whether a workspace mutation is already
// happening. That covers our own in-flight request, the progress modal
// the human's create/add-repos/fork/complete flows drive, and the
// interactive modals that are about to start one of those flows.
func (m Model) workspaceBusy() bool {
	if m.workspaceRequest != nil {
		return true
	}
	if m.wsProgress != nil && !m.wsProgress.Done {
		return true
	}
	switch m.mode {
	case ModeWorkspaceProgress, ModeTaskWizard, ModeRepoSelect, ModeForkDialog, ModeConfirmComplete:
		return true
	}
	return false
}

// startNextWorkspaceRequest launches the head of the queue when nothing
// else is mutating a workspace. Called after every Update, so a request
// starts on the first message following the modal that was holding it.
func (m Model) startNextWorkspaceRequest() (Model, tea.Cmd) {
	for len(m.workspaceQueue) > 0 && !m.workspaceBusy() {
		req := m.workspaceQueue[0]
		rest := make([]hooks.WorkspaceRequest, len(m.workspaceQueue)-1)
		copy(rest, m.workspaceQueue[1:])
		m.workspaceQueue = rest

		// A request whose caller has already given up is dropped
		// rather than started: work must never begin after the HTTP
		// side reported that nothing was applied.
		if req.Expired(time.Now()) {
			replyToWorkspaceRequest(req, hooks.WorkspaceFailure(req.Op, hooks.ReasonExpired, hooks.AppliedNo,
				"request expired in the queue before it could run"))
			m.appendDebugLog(fmt.Sprintf("[%s] workspace %s task=%s expired in queue",
				time.Now().Format("15:04:05"), req.Op, req.TaskName))
			continue
		}

		held := req
		m.workspaceRequest = &held
		m.workspaceRequestTask = m.displayRequestTask(req)
		m.workspaceRequestStarted = time.Now()

		// Starting is announced, not just finishing. The human's own
		// flows put a modal on screen the instant they begin; an agent's
		// request gets the same "this started" moment in the events row
		// the log renders, and the status line picks it up from the
		// in-flight fields above.
		m.appendDebugLog(fmt.Sprintf("[%s] workspace %s task=%s started",
			time.Now().Format("15:04:05"), req.Op, m.workspaceRequestTask))

		// The status line spins for as long as the request runs, and
		// nothing else is necessarily animating, so the tick starts here.
		return m, tea.Batch(m.runWorkspaceRequest(req), m.spinner.Tick)
	}
	return m, nil
}

// displayRequestTask resolves the name to show while a request runs. A
// caller that identified itself by cwd sends no name at all, and the
// status line has to say which task krang is changing.
func (m Model) displayRequestTask(req hooks.WorkspaceRequest) string {
	if req.TaskName != "" {
		return req.TaskName
	}
	if t := m.taskByWorkspaceCwd(req.Cwd); t != nil {
		return t.Name
	}
	return ""
}

// runWorkspaceRequest performs the mutation off the Update loop.
//
// The outcome is recorded — events-table row first, then the reply —
// so an operation whose HTTP caller has already timed out still leaves
// the same trail as one that was waited for. The reply channel is
// buffered, and the send is non-blocking regardless, so an abandoned
// caller can never wedge the TUI.
func (m Model) runWorkspaceRequest(req hooks.WorkspaceRequest) tea.Cmd {
	return func() tea.Msg {
		started := time.Now()
		resp, taskID := m.executeWorkspaceRequest(req)

		if taskID != "" {
			_ = m.eventStore.Log(taskID, workspaceEventType(req.Op), workspaceEventPayload(req, resp))
		}
		replyToWorkspaceRequest(req, resp)

		return workspaceRequestDoneMsg{
			Op:       req.Op,
			TaskName: req.TaskName,
			Response: resp,
			Elapsed:  time.Since(started),
			ReadOnly: req.Op.ReadOnly(),
		}
	}
}

// executeWorkspaceRequest does the actual work and returns the response
// plus the task the event row belongs to (empty when no task resolved).
func (m Model) executeWorkspaceRequest(req hooks.WorkspaceRequest) (hooks.WorkspaceResponse, string) {
	t, failure := m.resolveRequestTask(req)
	if t == nil {
		return failure, ""
	}

	var resp hooks.WorkspaceResponse
	switch req.Op {
	case hooks.WorkspaceOpList:
		resp = m.workspaceListOp(req, t)
	case hooks.WorkspaceOpRepos:
		resp = m.workspaceReposOp(req, t)
	case hooks.WorkspaceOpAdd:
		resp = m.workspaceAddOp(req, t)
	case hooks.WorkspaceOpRemoveSlot:
		resp = m.workspaceRemoveSlotOp(req, t)
	default:
		resp = hooks.WorkspaceFailure(req.Op, hooks.ReasonUnsupportedOperation, hooks.AppliedNo,
			fmt.Sprintf("krang does not implement workspace operation %q", req.Op))
	}

	// Every answer names the task it acted on, because a caller that
	// identified itself by cwd doesn't know what krang calls it.
	resp.Task = t.Name
	return resp, t.ID
}

// resolveRequestTask finds the task a workspace request is about. All
// four endpoints share it, so "which task?" means one thing across the
// API rather than one thing per endpoint.
//
// An explicit name wins outright. Otherwise the caller's cwd is matched
// against live tasks' workspace directories, longest match first, so an
// agent working inside a workspace never has to know what krang named
// it. The match is on the workspace directory rather than the task's
// tracked cwd because the cwd follows Claude around as it cd's, while
// the workspace directory is fixed for the life of the task.
//
// Returns a nil task and a ready-to-send failure when it can't decide.
func (m Model) resolveRequestTask(req hooks.WorkspaceRequest) (*db.Task, hooks.WorkspaceResponse) {
	if req.TaskName != "" {
		if t := m.taskByName(req.TaskName); t != nil {
			return t, hooks.WorkspaceResponse{}
		}
		return nil, hooks.WorkspaceFailure(req.Op, hooks.ReasonUnknownTask, hooks.AppliedNo,
			fmt.Sprintf("no live task named %q", req.TaskName))
	}

	if req.Cwd == "" {
		return nil, hooks.WorkspaceFailure(req.Op, hooks.ReasonInvalidRequest, hooks.AppliedNo,
			"the request named neither a task nor a cwd")
	}

	if t := m.taskByWorkspaceCwd(req.Cwd); t != nil {
		return t, hooks.WorkspaceResponse{}
	}
	return nil, hooks.WorkspaceFailure(req.Op, hooks.ReasonUnknownTask, hooks.AppliedNo,
		fmt.Sprintf("no live task owns a workspace containing %q; pass \"task\" explicitly", req.Cwd))
}

// taskByWorkspaceCwd returns the live task whose workspace directory
// contains cwd, preferring the longest match so a workspace nested
// inside another resolves to the inner one.
func (m Model) taskByWorkspaceCwd(cwd string) *db.Task {
	if m.taskStore == nil {
		return nil
	}
	tasks, err := m.taskStore.List()
	if err != nil {
		return nil
	}

	// Symlinked temp dirs (/var vs /private/var on macOS) mean the
	// caller's cwd and the stored path can spell the same directory
	// differently, so try the resolved form as well.
	candidates := []string{filepath.Clean(cwd)}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		resolved = filepath.Clean(resolved)
		if resolved != candidates[0] {
			candidates = append(candidates, resolved)
		}
	}

	var best *db.Task
	for i := range tasks {
		dir := tasks[i].WorkspaceDir
		if dir == "" {
			continue
		}
		dir = filepath.Clean(dir)
		if !anyPathWithin(candidates, dir) {
			continue
		}
		if best == nil || len(filepath.Clean(best.WorkspaceDir)) < len(dir) {
			best = &tasks[i]
		}
	}
	return best
}

// anyPathWithin reports whether any candidate path is dir itself or
// sits underneath it. The separator check is what keeps
// "workspaces/alpha-2" from matching the task owning "workspaces/alpha".
func anyPathWithin(candidates []string, dir string) bool {
	for _, candidate := range candidates {
		if candidate == dir || strings.HasPrefix(candidate, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// handleWorkspaceRequestDone records the finished request in the debug
// log and frees the in-flight slot.
func (m Model) handleWorkspaceRequestDone(msg workspaceRequestDoneMsg) (Model, tea.Cmd) {
	// A read-only request never took the slot, so it must not give one
	// back — the mutation that holds it is still running.
	if !msg.ReadOnly {
		m.workspaceRequest = nil
		m.workspaceRequestTask = ""
		m.workspaceRequestStarted = time.Time{}
	}

	line := fmt.Sprintf("[%s] workspace %s task=%s %s",
		time.Now().Format("15:04:05"), msg.Op, msg.TaskName, msg.Response.Status)
	if msg.Response.Reason != "" {
		line += " reason=" + msg.Response.Reason
	}
	line += fmt.Sprintf(" in %s", msg.Elapsed.Round(time.Millisecond))
	m.appendDebugLog(line)

	// No refresh: workspace mutations don't change the task table, and
	// the events row plus this log line are the durable record. An
	// operation that does change task state should return
	// m.refreshTasks from here.
	return m, nil
}

// taskByName resolves a live (non-terminal) task by name. Callers
// outside the TUI know task names, not IDs.
func (m Model) taskByName(name string) *db.Task {
	if m.taskStore == nil || name == "" {
		return nil
	}
	tasks, err := m.taskStore.List()
	if err != nil {
		return nil
	}
	for i := range tasks {
		if tasks[i].Name == name {
			return &tasks[i]
		}
	}
	return nil
}

// noteWorkspaceRequestBusy tells the human why a keyboard workspace
// flow was refused. The agent's request holds the same in-flight slot
// the keyboard flows use, so the two can never interleave.
func (m *Model) noteWorkspaceRequestBusy() {
	if m.workspaceRequest == nil {
		return
	}
	m.appendDebugLog(fmt.Sprintf("[%s] workspace busy: %s request for %s in flight",
		time.Now().Format("15:04:05"), m.workspaceRequest.Op, m.workspaceRequest.TaskName))
}

// workspaceEventType names the events-table row for an operation.
func workspaceEventType(op hooks.WorkspaceOp) string {
	return "workspace_" + string(op)
}

func workspaceEventPayload(req hooks.WorkspaceRequest, resp hooks.WorkspaceResponse) string {
	payload := struct {
		Op      hooks.WorkspaceOp `json:"op"`
		Task    string            `json:"task"`
		Repo    string            `json:"repo,omitempty"`
		Label   string            `json:"label,omitempty"`
		Dir     string            `json:"dir,omitempty"`
		Base    string            `json:"base,omitempty"`
		Force   bool              `json:"force,omitempty"`
		Status  string            `json:"status"`
		Reason  string            `json:"reason,omitempty"`
		Applied string            `json:"applied,omitempty"`
	}{
		Op: req.Op,
		// The resolved name, not the requested one: a cwd-identified
		// caller sends no name at all, and the row has to say which
		// task the operation actually touched.
		Task:    resp.Task,
		Repo:    req.Repo,
		Label:   req.Label,
		Dir:     req.Dir,
		Base:    req.Base,
		Force:   req.Force,
		Status:  resp.Status,
		Reason:  resp.Reason,
		Applied: resp.Applied,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(data)
}

// replyToWorkspaceRequest answers the caller without ever blocking.
func replyToWorkspaceRequest(req hooks.WorkspaceRequest, resp hooks.WorkspaceResponse) {
	if req.Reply == nil {
		return
	}
	select {
	case req.Reply <- resp:
	default:
		// Capacity-1 channel with a single sender: a full buffer
		// would mean a duplicate reply, which can't happen. Dropping
		// beats deadlocking the TUI either way.
	}
}
