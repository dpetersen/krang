package task

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/tmux"
	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
)

const gracefulShutdownTimeout = 5 * time.Second

type Manager struct {
	tasks         *db.TaskStore
	events        *db.EventStore
	activeSession string
}

func NewManager(tasks *db.TaskStore, events *db.EventStore, activeSession string) *Manager {
	return &Manager{tasks: tasks, events: events, activeSession: activeSession}
}

func (m *Manager) CreateTask(name, prompt, cwd string) (*db.Task, error) {
	taskID := ulid.Make().String()
	sessionID := uuid.New().String()

	task := &db.Task{
		ID:        taskID,
		Name:      name,
		Prompt:    prompt,
		State:     db.StateActive,
		Attention: db.AttentionOK,
		SessionID: sessionID,
		Cwd:       cwd,
	}

	claudeCmd := fmt.Sprintf(
		"cd %s && claude --session-id %s --name %s",
		shellQuote(cwd),
		sessionID,
		shellQuote(name),
	)

	windowName := tmux.WindowName(name)
	windowID, err := tmux.CreateWindow(m.activeSession, windowName, claudeCmd)
	if err != nil {
		return nil, fmt.Errorf("creating tmux window: %w", err)
	}
	task.TmuxWindow = windowID

	if err := m.tasks.Create(task); err != nil {
		tmux.KillWindow(windowID)
		return nil, fmt.Errorf("saving task: %w", err)
	}

	if prompt != "" {
		if err := tmux.SendKeys(windowID, prompt); err != nil {
			_ = m.events.Log(taskID, "send_keys_failed", err.Error())
		}
	}

	return task, nil
}

func (m *Manager) Park(taskID string) error {
	tasks, err := m.tasks.List()
	if err != nil {
		return err
	}

	task := findTask(tasks, taskID)
	if task == nil {
		return fmt.Errorf("task not found: %s", taskID)
	}
	if task.State != db.StateActive {
		return fmt.Errorf("task %s is not active (state: %s)", task.Name, task.State)
	}

	if err := tmux.MoveWindow(task.TmuxWindow, tmux.ParkedSession); err != nil {
		return fmt.Errorf("moving window to parked: %w", err)
	}

	return m.tasks.UpdateState(task.ID, db.StateParked)
}

func (m *Manager) Unpark(taskID string) error {
	tasks, err := m.tasks.List()
	if err != nil {
		return err
	}

	task := findTask(tasks, taskID)
	if task == nil {
		return fmt.Errorf("task not found: %s", taskID)
	}
	if task.State != db.StateParked {
		return fmt.Errorf("task %s is not parked (state: %s)", task.Name, task.State)
	}

	if err := tmux.MoveWindow(task.TmuxWindow, m.activeSession); err != nil {
		return fmt.Errorf("moving window to active: %w", err)
	}

	return m.tasks.UpdateState(task.ID, db.StateActive)
}

func (m *Manager) Dormify(taskID string) error {
	tasks, err := m.tasks.List()
	if err != nil {
		return err
	}

	task := findTask(tasks, taskID)
	if task == nil {
		return fmt.Errorf("task not found: %s", taskID)
	}
	if task.State != db.StateParked && task.State != db.StateActive {
		return fmt.Errorf("task %s cannot be dormified (state: %s)", task.Name, task.State)
	}

	if task.TmuxWindow != "" {
		m.gracefulCloseWindow(task)
	}

	if err := m.tasks.UpdateTmuxWindow(task.ID, ""); err != nil {
		return err
	}

	return m.tasks.UpdateState(task.ID, db.StateDormant)
}

func (m *Manager) Wake(taskID string) error {
	tasks, err := m.tasks.ListAll()
	if err != nil {
		return err
	}

	task := findTask(tasks, taskID)
	if task == nil {
		return fmt.Errorf("task not found: %s", taskID)
	}
	if task.State != db.StateDormant {
		return fmt.Errorf("task %s is not dormant (state: %s)", task.Name, task.State)
	}
	if task.SessionID == "" {
		return fmt.Errorf("task %s has no session ID to resume", task.Name)
	}

	claudeCmd := fmt.Sprintf(
		"cd %s && claude --resume %s --name %s",
		shellQuote(task.Cwd),
		task.SessionID,
		shellQuote(task.Name),
	)

	windowName := tmux.WindowName(task.Name)
	windowID, err := tmux.CreateWindow(m.activeSession, windowName, claudeCmd)
	if err != nil {
		return fmt.Errorf("creating tmux window for wake: %w", err)
	}

	if err := m.tasks.UpdateTmuxWindow(task.ID, windowID); err != nil {
		return err
	}

	return m.tasks.UpdateState(task.ID, db.StateActive)
}

func (m *Manager) Kill(taskID string) error {
	tasks, err := m.tasks.ListAll()
	if err != nil {
		return err
	}

	task := findTask(tasks, taskID)
	if task == nil {
		return fmt.Errorf("task not found: %s", taskID)
	}

	if task.TmuxWindow != "" {
		m.gracefulCloseWindow(task)
	}

	if err := m.tasks.UpdateTmuxWindow(task.ID, ""); err != nil {
		return err
	}

	return m.tasks.UpdateState(task.ID, db.StateFailed)
}

func (m *Manager) Complete(taskID string) error {
	tasks, err := m.tasks.ListAll()
	if err != nil {
		return err
	}

	task := findTask(tasks, taskID)
	if task == nil {
		return fmt.Errorf("task not found: %s", taskID)
	}

	if task.TmuxWindow != "" {
		m.gracefulCloseWindow(task)
	}

	if err := m.tasks.UpdateTmuxWindow(task.ID, ""); err != nil {
		return err
	}
	if err := m.tasks.UpdateAttention(task.ID, db.AttentionDone); err != nil {
		return err
	}

	return m.tasks.UpdateState(task.ID, db.StateCompleted)
}

// gracefulCloseWindow finds the Claude process in the tmux pane, sends
// SIGINT for graceful shutdown (triggers SessionEnd hooks), waits for
// the window to close, and falls back to kill-window if it doesn't.
func (m *Manager) gracefulCloseWindow(task *db.Task) {
	shellPID, err := tmux.PanePID(task.TmuxWindow)
	if err != nil {
		_ = m.events.Log(task.ID, "shutdown_warning", "could not get pane PID: "+err.Error())
		_ = tmux.KillWindow(task.TmuxWindow)
		return
	}

	claudePID := findClaudeChild(shellPID)
	if claudePID > 0 {
		_ = syscall.Kill(claudePID, syscall.SIGINT)
	} else {
		// No Claude child found — maybe it already exited.
		_ = m.events.Log(task.ID, "shutdown_warning", "no claude child process found")
	}

	deadline := time.Now().Add(gracefulShutdownTimeout)
	for time.Now().Before(deadline) {
		if !tmux.WindowExists(task.TmuxWindow) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}

	_ = m.events.Log(task.ID, "graceful_shutdown_timeout", "fell back to kill-window")
	_ = tmux.KillWindow(task.TmuxWindow)
}

// findClaudeChild looks for a claude/node process that is a child of
// the given shell PID.
func findClaudeChild(shellPID int) int {
	out, err := exec.Command("pgrep", "-P", fmt.Sprintf("%d", shellPID)).Output()
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var pid int
		if _, err := fmt.Sscanf(line, "%d", &pid); err != nil {
			continue
		}
		return pid
	}
	return 0
}

func (m *Manager) Focus(taskID string) error {
	tasks, err := m.tasks.List()
	if err != nil {
		return err
	}

	task := findTask(tasks, taskID)
	if task == nil {
		return fmt.Errorf("task not found: %s", taskID)
	}
	if task.TmuxWindow == "" {
		return fmt.Errorf("task %s has no tmux window", task.Name)
	}

	return tmux.SelectWindow(task.TmuxWindow)
}

func (m *Manager) ListTasks() ([]db.Task, error) {
	return m.tasks.List()
}

func findTask(tasks []db.Task, idOrName string) *db.Task {
	for i := range tasks {
		if tasks[i].ID == idOrName || tasks[i].Name == idOrName {
			return &tasks[i]
		}
		if strings.HasPrefix(tasks[i].ID, idOrName) {
			return &tasks[i]
		}
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
