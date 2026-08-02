package tui

import (
	"encoding/json"
	"fmt"
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

// handleWorkspaceRequest queues an arriving request. It deliberately
// does no work: startNextWorkspaceRequest runs after every message and
// picks the request up as soon as the workspace is free.
func (m Model) handleWorkspaceRequest(req hooks.WorkspaceRequest) (Model, tea.Cmd) {
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
		return m, m.runWorkspaceRequest(req)
	}
	return m, nil
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
		}
	}
}

// executeWorkspaceRequest does the actual work and returns the response
// plus the task the event row belongs to (empty when no task resolved).
func (m Model) executeWorkspaceRequest(req hooks.WorkspaceRequest) (hooks.WorkspaceResponse, string) {
	t := m.taskByName(req.TaskName)
	if t == nil {
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonUnknownTask, hooks.AppliedNo,
			fmt.Sprintf("no live task named %q", req.TaskName)), ""
	}

	switch req.Op {
	case hooks.WorkspaceOpPing:
		// Scaffolding: no mutation, just proof that the request
		// reached the TUI and resolved a task.
		return hooks.WorkspaceOK(req.Op, map[string]string{
			"task":          t.Name,
			"task_id":       t.ID,
			"workspace_dir": t.WorkspaceDir,
			"echo":          req.Message,
		}), t.ID

	default:
		return hooks.WorkspaceFailure(req.Op, hooks.ReasonUnsupportedOperation, hooks.AppliedNo,
			fmt.Sprintf("krang does not implement workspace operation %q", req.Op)), t.ID
	}
}

// handleWorkspaceRequestDone records the finished request in the debug
// log and frees the in-flight slot.
func (m Model) handleWorkspaceRequestDone(msg workspaceRequestDoneMsg) (Model, tea.Cmd) {
	m.workspaceRequest = nil

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
		Status  string            `json:"status"`
		Reason  string            `json:"reason,omitempty"`
		Applied string            `json:"applied,omitempty"`
	}{
		Op:      req.Op,
		Task:    req.TaskName,
		Repo:    req.Repo,
		Label:   req.Label,
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
