package tui

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/hooks"
)

// newWorkspaceReqModel builds a Model backed by a throwaway database
// holding one live task named "alpha". The manager, tmux, and hook
// channel are all absent — the request path touches none of them.
func newWorkspaceReqModel(t *testing.T) (Model, *sql.DB) {
	t.Helper()

	t.Setenv("KRANG_DB", filepath.Join(t.TempDir(), "krang.db"))
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	taskStore := db.NewTaskStore(database)
	if err := taskStore.Create(&db.Task{
		ID: "01ALPHA", Name: "alpha", State: db.StateActive,
		Attention: db.AttentionOK, Cwd: "/tmp/ws/alpha", WorkspaceDir: "/tmp/ws/alpha",
	}); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	return Model{
		taskStore:  taskStore,
		eventStore: db.NewEventStore(database),
	}, database
}

// runCmd executes a command and flattens any batch it produces, so a
// test can drive the same work Bubble Tea would. Safe here because the
// only commands these paths produce are the request runner and the
// (nil) channel re-arm.
func runCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()

	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var msgs []tea.Msg
		for _, c := range batch {
			msgs = append(msgs, runCmd(t, c)...)
		}
		return msgs
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

func doneMsg(t *testing.T, msgs []tea.Msg) workspaceRequestDoneMsg {
	t.Helper()

	for _, msg := range msgs {
		if done, ok := msg.(workspaceRequestDoneMsg); ok {
			return done
		}
	}
	t.Fatalf("no workspaceRequestDoneMsg among %d messages", len(msgs))
	return workspaceRequestDoneMsg{}
}

func replyOrFail(t *testing.T, req hooks.WorkspaceRequest) hooks.WorkspaceResponse {
	t.Helper()

	select {
	case resp := <-req.Reply:
		return resp
	default:
		t.Fatal("no reply on the request channel")
		return hooks.WorkspaceResponse{}
	}
}

func noReplyYet(t *testing.T, req hooks.WorkspaceRequest, what string) {
	t.Helper()

	select {
	case resp := <-req.Reply:
		t.Fatalf("%s: got an early reply %+v", what, resp)
	default:
	}
}

func pingRequest(message string) hooks.WorkspaceRequest {
	req := hooks.NewWorkspaceRequest(hooks.WorkspaceOpPing, "alpha")
	req.Message = message
	return req
}

func countEvents(t *testing.T, database *sql.DB, eventType string) int {
	t.Helper()

	var count int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM events WHERE event_type = ?", eventType,
	).Scan(&count); err != nil {
		t.Fatalf("counting events: %v", err)
	}
	return count
}

// Exactly one mutation runs at a time: a request arriving while another
// is in flight waits in the queue, starts when the first finishes, and
// both callers get their own reply.
func TestWorkspaceRequestQueuesBehindInFlightRequest(t *testing.T) {
	m, database := newWorkspaceReqModel(t)

	first := pingRequest("first")
	second := pingRequest("second")

	model, firstCmd := m.Update(workspaceRequestMsg{Request: first})
	m = model.(Model)
	if m.workspaceRequest == nil {
		t.Fatal("first request did not start")
	}

	// Arrives mid-flight: queued, not started, not answered.
	model, queuedCmd := m.Update(workspaceRequestMsg{Request: second})
	m = model.(Model)
	if len(m.workspaceQueue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(m.workspaceQueue))
	}
	if m.workspaceRequest.Message != "first" {
		t.Errorf("in-flight request = %q, want the first one", m.workspaceRequest.Message)
	}
	if runCmd(t, queuedCmd) != nil {
		t.Error("queuing a request produced work; it must wait for the in-flight one")
	}
	noReplyYet(t, second, "second request while the first is in flight")

	// The first request finishes.
	firstDone := doneMsg(t, runCmd(t, firstCmd))
	firstResp := replyOrFail(t, first)
	if firstResp.Status != hooks.WorkspaceStatusOK || firstResp.Data["echo"] != "first" {
		t.Errorf("first reply = %+v, want ok echoing %q", firstResp, "first")
	}

	model, secondCmd := m.Update(firstDone)
	m = model.(Model)
	if len(m.workspaceQueue) != 0 {
		t.Fatalf("queue length = %d, want 0 after the slot freed", len(m.workspaceQueue))
	}
	if m.workspaceRequest == nil || m.workspaceRequest.Message != "second" {
		t.Fatalf("in-flight request = %+v, want the queued second one", m.workspaceRequest)
	}

	secondDone := doneMsg(t, runCmd(t, secondCmd))
	secondResp := replyOrFail(t, second)
	if secondResp.Status != hooks.WorkspaceStatusOK || secondResp.Data["echo"] != "second" {
		t.Errorf("second reply = %+v, want ok echoing %q", secondResp, "second")
	}

	model, _ = m.Update(secondDone)
	m = model.(Model)
	if m.workspaceRequest != nil {
		t.Error("in-flight slot still held after the last request finished")
	}
	if got := countEvents(t, database, "workspace_ping"); got != 2 {
		t.Errorf("workspace_ping events = %d, want 2", got)
	}
}

// The human's own workspace flow holds the same in-flight slot, so a
// request that lands while a clone is running waits for it.
func TestWorkspaceRequestQueuesBehindHumanWorkspaceClone(t *testing.T) {
	m, _ := newWorkspaceReqModel(t)
	m.mode = ModeWorkspaceProgress
	m.wsProgress = &wsProgressState{Title: "Creating workspace \"beta\""}

	req := pingRequest("during clone")

	model, cmd := m.Update(workspaceRequestMsg{Request: req})
	m = model.(Model)
	if m.workspaceRequest != nil {
		t.Fatal("request started while the human's clone was in flight")
	}
	if len(m.workspaceQueue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(m.workspaceQueue))
	}
	if runCmd(t, cmd) != nil {
		t.Error("queued request produced work while the workspace was busy")
	}
	noReplyYet(t, req, "request during the human's clone")

	// The clone finishes and the modal closes. The very next message
	// releases the queue.
	m.wsProgress.Done = true
	m.mode = ModeNormal

	model, cmd = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = model.(Model)
	if m.workspaceRequest == nil {
		t.Fatal("request did not start once the workspace was free")
	}

	done := doneMsg(t, runCmd(t, cmd))
	if resp := replyOrFail(t, req); resp.Status != hooks.WorkspaceStatusOK {
		t.Errorf("reply = %+v, want ok", resp)
	}
	if done.Op != hooks.WorkspaceOpPing {
		t.Errorf("done op = %q, want %q", done.Op, hooks.WorkspaceOpPing)
	}
}

// Interactive modals that are about to start a workspace flow count as
// busy too, so a request can't slip in between the form and its submit.
func TestWorkspaceRequestQueuesBehindInteractiveModal(t *testing.T) {
	for _, mode := range []InputMode{ModeTaskWizard, ModeRepoSelect, ModeForkDialog, ModeConfirmComplete} {
		m, _ := newWorkspaceReqModel(t)
		m.mode = mode

		req := pingRequest("during modal")
		model, _ := m.Update(workspaceRequestMsg{Request: req})
		m = model.(Model)

		if m.workspaceRequest != nil {
			t.Errorf("mode %v: request started with a workspace modal open", mode)
		}
		if len(m.workspaceQueue) != 1 {
			t.Errorf("mode %v: queue length = %d, want 1", mode, len(m.workspaceQueue))
		}
	}
}

// AC: every completed mutation leaves an events row and a debug-log
// line behind.
func TestCompletedWorkspaceRequestRecordsEventAndDebugLog(t *testing.T) {
	m, database := newWorkspaceReqModel(t)

	req := pingRequest("hello")

	model, cmd := m.Update(workspaceRequestMsg{Request: req})
	m = model.(Model)
	done := doneMsg(t, runCmd(t, cmd))

	// The events row is written before the caller is answered, so an
	// abandoned request is still recorded.
	if got := countEvents(t, database, "workspace_ping"); got != 1 {
		t.Errorf("workspace_ping events = %d, want 1", got)
	}
	var payload string
	if err := database.QueryRow(
		"SELECT payload FROM events WHERE event_type = 'workspace_ping'",
	).Scan(&payload); err != nil {
		t.Fatalf("reading event payload: %v", err)
	}
	for _, want := range []string{`"op":"ping"`, `"task":"alpha"`, `"status":"ok"`} {
		if !strings.Contains(payload, want) {
			t.Errorf("event payload %s missing %s", payload, want)
		}
	}

	model, _ = m.Update(done)
	m = model.(Model)

	logged := strings.Join(m.debugLog, "\n")
	if !strings.Contains(logged, "workspace ping task=alpha ok") {
		t.Errorf("debug log %q missing the completion line", logged)
	}
}

// A request naming a task krang doesn't have fails cleanly with a
// machine-readable reason and no events row to attach it to.
func TestWorkspaceRequestUnknownTaskFails(t *testing.T) {
	m, database := newWorkspaceReqModel(t)

	req := hooks.NewWorkspaceRequest(hooks.WorkspaceOpPing, "ghost")

	model, cmd := m.Update(workspaceRequestMsg{Request: req})
	m = model.(Model)
	runCmd(t, cmd)

	resp := replyOrFail(t, req)
	if resp.Reason != hooks.ReasonUnknownTask {
		t.Errorf("reason = %q, want %q", resp.Reason, hooks.ReasonUnknownTask)
	}
	if resp.Applied != hooks.AppliedNo {
		t.Errorf("applied = %q, want %q", resp.Applied, hooks.AppliedNo)
	}
	if got := countEvents(t, database, "workspace_ping"); got != 0 {
		t.Errorf("workspace_ping events = %d, want 0 for an unresolved task", got)
	}
}

// A caller that has already timed out must never have its work start
// afterwards: queued requests are dropped at their deadline.
func TestExpiredQueuedWorkspaceRequestIsDroppedNotStarted(t *testing.T) {
	m, database := newWorkspaceReqModel(t)
	m.mode = ModeWorkspaceProgress
	m.wsProgress = &wsProgressState{Title: "busy"}

	req := pingRequest("abandoned")
	req.Deadline = time.Now().Add(20 * time.Millisecond)

	model, _ := m.Update(workspaceRequestMsg{Request: req})
	m = model.(Model)
	if len(m.workspaceQueue) != 1 {
		t.Fatalf("queue length = %d, want 1", len(m.workspaceQueue))
	}

	time.Sleep(30 * time.Millisecond)
	m.wsProgress.Done = true
	m.mode = ModeNormal

	model, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = model.(Model)

	if m.workspaceRequest != nil {
		t.Error("an expired request was started after its caller gave up")
	}
	if cmd != nil {
		t.Error("an expired request produced work")
	}
	if resp := replyOrFail(t, req); resp.Reason != hooks.ReasonExpired {
		t.Errorf("reason = %q, want %q", resp.Reason, hooks.ReasonExpired)
	}
	if got := countEvents(t, database, "workspace_ping"); got != 0 {
		t.Errorf("workspace_ping events = %d, want 0 for a dropped request", got)
	}
}

// The keyboard flows share the in-flight guard: a request holding it
// keeps the human out rather than letting the two interleave.
func TestKeyboardWorkspaceFlowsRefusedWhileRequestInFlight(t *testing.T) {
	held := hooks.NewWorkspaceRequest(hooks.WorkspaceOpPing, "alpha")

	for _, key := range []string{"d", "e", "c"} {
		m := Model{
			mode:             ModeDetail,
			tasks:            []db.Task{{ID: "01ALPHA", Name: "alpha", State: db.StateActive, SessionID: "sess", WorkspaceDir: "/tmp/ws/alpha"}},
			workspaceRequest: &held,
		}

		model, cmd := m.handleDetailKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		next := model.(Model)

		if next.mode != ModeDetail {
			t.Errorf("key %q: mode = %v, want the detail modal to stay put", key, next.mode)
		}
		if cmd != nil {
			t.Errorf("key %q: started work while a workspace request was in flight", key)
		}
		if len(next.debugLog) == 0 {
			t.Errorf("key %q: refusal was not explained in the debug log", key)
		}
	}
}
