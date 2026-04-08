package task

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/dpetersen/krang/internal/config"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/pathutil"
	"github.com/dpetersen/krang/internal/tmux"
	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
)

const gracefulShutdownTimeout = 15 * time.Second

type Manager struct {
	tasks           *db.TaskStore
	events          *db.EventStore
	activeSession   string
	parkedSession   string
	sandboxProfiles map[string]config.SandboxProfile
	defaultSandbox  string
	stateFilePath   string
	metarepoDir     string
	reposDir        string
}

func NewManager(tasks *db.TaskStore, events *db.EventStore, activeSession, parkedSession string, sandboxProfiles map[string]config.SandboxProfile, defaultSandbox, stateFilePath, metarepoDir, reposDir string) *Manager {
	return &Manager{tasks: tasks, events: events, activeSession: activeSession, parkedSession: parkedSession, sandboxProfiles: sandboxProfiles, defaultSandbox: defaultSandbox, stateFilePath: stateFilePath, metarepoDir: metarepoDir, reposDir: reposDir}
}

func (m *Manager) resolveSandboxCommand(profileName string) string {
	name := profileName
	if name == "" {
		name = m.defaultSandbox
	}
	if name == "" {
		return ""
	}
	profile, ok := m.sandboxProfiles[name]
	if !ok || profile.Type != "command" {
		return ""
	}
	return profile.Command
}

func (m *Manager) templateData(taskName, taskCwd string) sandboxTemplateData {
	return sandboxTemplateData{
		KrangDir: m.metarepoDir,
		TaskCwd:  taskCwd,
		TaskName: taskName,
		ReposDir: m.reposDir,
	}
}

type sandboxTemplateData struct {
	KrangDir string
	TaskCwd  string
	TaskName string
	ReposDir string
}

func expandSandboxCommand(sandboxCommand string, data sandboxTemplateData) string {
	tmpl, err := template.New("sandbox").Parse(sandboxCommand)
	if err != nil {
		return sandboxCommand
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return sandboxCommand
	}
	return buf.String()
}

// buildClaudeCommand constructs the shell command to launch Claude.
// forkFrom is the source session ID when forking (not the name).
func buildClaudeCommand(sessionID, name string, flags db.TaskFlags, resume bool, sandboxCommand, stateFilePath string, tmplData sandboxTemplateData, forkFrom string) string {
	var cmd string
	if stateFilePath != "" {
		cmd = "export KRANG_STATEFILE=" + shellQuote(stateFilePath) + "; "
	}

	claudeBin := os.Getenv("KRANG_CLAUDE_CMD")
	if claudeBin == "" {
		claudeBin = "claude"
	}

	if sandboxCommand == "" {
		cmd += claudeBin
	} else {
		expanded := expandSandboxCommand(sandboxCommand, tmplData)
		cmd += expanded + " " + claudeBin
	}

	if forkFrom != "" {
		cmd += " --resume " + shellQuote(forkFrom) + " --fork-session"
		cmd += " --name " + shellQuote(name)
	} else if resume {
		cmd += " --resume " + shellQuote(sessionID)
	} else {
		cmd += " --session-id " + sessionID
		cmd += " --name " + shellQuote(name)
	}

	if flags.Debug {
		cmd = "export KRANG_DEBUG=1; " + cmd
	}

	if flags.DangerouslySkipPermissions {
		cmd += " --dangerously-skip-permissions"
	}

	cmd += "; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	return cmd
}

func (m *Manager) CreateTask(name, prompt, cwd string, flags db.TaskFlags, sandboxProfile string) (*db.Task, error) {
	taskID := ulid.Make().String()
	sessionID := uuid.New().String()

	task := &db.Task{
		ID:             taskID,
		Name:           name,
		Prompt:         prompt,
		State:          db.StateActive,
		Attention:      db.AttentionOK,
		SessionID:      sessionID,
		Cwd:            cwd,
		Flags:          flags,
		SandboxProfile: sandboxProfile,
	}

	sandboxCmd := m.resolveSandboxCommand(sandboxProfile)
	claudeCmd := buildClaudeCommand(sessionID, name, flags, false, sandboxCmd, m.stateFilePath, m.templateData(name, cwd), "")

	windowName := tmux.WindowName(name)
	windowID, err := tmux.CreateWindow(m.activeSession, windowName, cwd, claudeCmd)
	if err != nil {
		return nil, fmt.Errorf("creating tmux window: %w", err)
	}
	_ = tmux.SetWindowOption(windowID, "krang-task", name)
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

func (m *Manager) ImportTask(name, sessionID string) error {
	taskID := ulid.Make().String()

	cwd, err := findSessionCwd(sessionID)
	if err != nil {
		return fmt.Errorf("could not find session %s in Claude projects: %w", sessionID, err)
	}

	task := &db.Task{
		ID:        taskID,
		Name:      name,
		State:     db.StateDormant,
		Attention: db.AttentionOK,
		SessionID: sessionID,
		Cwd:       cwd,
	}

	return m.tasks.Create(task)
}

// findSessionCwd searches ~/.claude/projects/ for a session ID file
// and decodes the cwd from the containing directory name.
func findSessionCwd(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	projectsDir := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", fmt.Errorf("reading projects dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionFile := filepath.Join(projectsDir, entry.Name(), sessionID+".jsonl")
		if _, err := os.Stat(sessionFile); err == nil {
			return decodeCwdFromDirName(entry.Name()), nil
		}
	}

	return "", fmt.Errorf("session %s not found in any project directory", sessionID)
}

// encodePath encodes a path the same way Claude does for project
// directory names: replace all non-alphanumeric chars with '-'.
func encodePath(path string) string {
	return pathutil.EncodePath(path)
}

// decodeCwdFromDirName reconstructs the original filesystem path from
// a Claude project directory name by walking the filesystem and
// matching encoded directory names at each level.
func decodeCwdFromDirName(encoded string) string {
	// Walk the filesystem greedily, matching the longest directory
	// name at each level whose encoding matches the next segment.
	result := resolveEncoded("/", encoded[1:]) // skip leading -
	if result != "" {
		return result
	}
	// Fallback: naive decode.
	return "/" + strings.ReplaceAll(encoded[1:], "-", "/")
}

func resolveEncoded(currentPath, remaining string) string {
	if remaining == "" {
		return currentPath
	}

	entries, err := os.ReadDir(currentPath)
	if err != nil {
		return ""
	}

	// Try longest matches first to prefer "vercel-marketplace-integration"
	// over "vercel" + "marketplace" + "integration".
	type candidate struct {
		name    string
		restLen int
	}
	var candidates []candidate

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		entryEncoded := encodePath(entry.Name())
		if strings.HasPrefix(remaining, entryEncoded) {
			rest := remaining[len(entryEncoded):]
			// rest must be empty (exact match) or start with -
			// (path separator).
			if rest == "" || rest[0] == '-' {
				if rest != "" {
					rest = rest[1:] // skip the separator -
				}
				candidates = append(candidates, candidate{entry.Name(), len(rest)})
			}
		}
	}

	// Sort by shortest remaining (longest match first).
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].restLen < candidates[j].restLen
	})

	for _, c := range candidates {
		rest := remaining[len(encodePath(c.name)):]
		if rest != "" {
			rest = rest[1:]
		}
		result := resolveEncoded(filepath.Join(currentPath, c.name), rest)
		if result != "" {
			return result
		}
	}

	return ""
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

	if err := tmux.MoveWindow(task.TmuxWindow, m.parkedSession); err != nil {
		return fmt.Errorf("moving window to parked: %w", err)
	}
	for _, cID := range tmux.FindCompanions(m.activeSession, task.Name) {
		_ = tmux.MoveWindow(cID, m.parkedSession)
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
	for _, cID := range tmux.FindCompanions(m.parkedSession, task.Name) {
		_ = tmux.MoveWindow(cID, m.activeSession)
	}

	return m.tasks.UpdateState(task.ID, db.StateActive)
}

func (m *Manager) Dormify(taskID string) error {
	tasks, err := m.tasks.List()
	if err != nil {
		return fmt.Errorf("dormify list: %w", err)
	}

	task := findTask(tasks, taskID)
	if task == nil {
		return fmt.Errorf("task not found: %s", taskID)
	}
	if task.State != db.StateParked && task.State != db.StateActive {
		return fmt.Errorf("task %s cannot be dormified (state: %s)", task.Name, task.State)
	}

	for _, session := range []string{m.activeSession, m.parkedSession} {
		for _, cID := range tmux.FindCompanions(session, task.Name) {
			_ = tmux.KillWindow(cID)
		}
	}

	// Kill the window before updating state. If the window can't be
	// killed, the task should not transition to dormant.
	if task.TmuxWindow != "" {
		if err := m.gracefulCloseWindow(task); err != nil {
			return fmt.Errorf("dormify close window: %w", err)
		}
	}

	if err := m.tasks.UpdateState(task.ID, db.StateDormant); err != nil {
		return fmt.Errorf("dormify state update: %w", err)
	}
	if err := m.tasks.UpdateTmuxWindow(task.ID, ""); err != nil {
		return fmt.Errorf("dormify clear window: %w", err)
	}

	return nil
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

	sandboxCmd := m.resolveSandboxCommand(task.SandboxProfile)
	claudeCmd := buildClaudeCommand(task.SessionID, task.Name, task.Flags, true, sandboxCmd, m.stateFilePath, m.templateData(task.Name, task.Cwd), "")

	// Use the session's original project directory rather than the
	// live cwd, which may have drifted as Claude cd'd around. Claude
	// resolves sessions relative to the project directory, so launching
	// from the wrong cwd causes "session not found" errors.
	launchCwd := task.Cwd
	if sessionCwd, err := findSessionCwd(task.SessionID); err == nil {
		launchCwd = sessionCwd
	}

	windowName := tmux.WindowName(task.Name)
	windowID, err := tmux.CreateWindow(m.activeSession, windowName, launchCwd, claudeCmd)
	if err != nil {
		return fmt.Errorf("creating tmux window for wake: %w", err)
	}
	_ = tmux.SetWindowOption(windowID, "krang-task", task.Name)

	if err := m.tasks.UpdateTmuxWindow(task.ID, windowID); err != nil {
		return err
	}

	return m.tasks.UpdateState(task.ID, db.StateActive)
}

func (m *Manager) Relaunch(taskID string) error {
	tasks, err := m.tasks.ListAll()
	if err != nil {
		return err
	}

	task := findTask(tasks, taskID)
	if task == nil {
		return fmt.Errorf("task not found: %s", taskID)
	}
	if task.State != db.StateActive && task.State != db.StateParked {
		return fmt.Errorf("task %s cannot be relaunched (state: %s)", task.Name, task.State)
	}

	// If parked, unpark first.
	if task.State == db.StateParked {
		if err := m.Unpark(taskID); err != nil {
			return fmt.Errorf("unparking for relaunch: %w", err)
		}
	}

	// Shut down Claude and wait for the process to exit so the
	// session file is fully persisted before we --resume it.
	if task.TmuxWindow != "" {
		m.shutdownClaudeForRelaunch(task)
	}

	// Build new command with --resume and current flags.
	sandboxCmd := m.resolveSandboxCommand(task.SandboxProfile)
	claudeCmd := buildClaudeCommand(task.SessionID, task.Name, task.Flags, true, sandboxCmd, m.stateFilePath, m.templateData(task.Name, task.Cwd), "")

	// Use the session's original project directory (see Thaw for rationale).
	launchCwd := task.Cwd
	if sessionCwd, err := findSessionCwd(task.SessionID); err == nil {
		launchCwd = sessionCwd
	}

	windowName := tmux.WindowName(task.Name)
	windowID, err := tmux.CreateWindow(m.activeSession, windowName, launchCwd, claudeCmd)
	if err != nil {
		return fmt.Errorf("creating tmux window for relaunch: %w", err)
	}
	_ = tmux.SetWindowOption(windowID, "krang-task", task.Name)

	if err := m.tasks.UpdateTmuxWindow(task.ID, windowID); err != nil {
		return err
	}

	return m.tasks.UpdateState(task.ID, db.StateActive)
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

	// Kill the window before updating state. If the window can't be
	// killed, the task should not transition to completed.
	if task.TmuxWindow != "" {
		if err := m.gracefulCloseWindow(task); err != nil {
			return fmt.Errorf("closing window: %w", err)
		}
	}

	if err := m.tasks.UpdateState(task.ID, db.StateCompleted); err != nil {
		return err
	}
	if err := m.tasks.UpdateAttention(task.ID, db.AttentionDone); err != nil {
		return err
	}
	if err := m.tasks.UpdateTmuxWindow(task.ID, ""); err != nil {
		return err
	}

	return nil
}

// gracefulCloseWindow finds the Claude process in the tmux pane, sends
// SIGINT for graceful shutdown (triggers SessionEnd hooks), waits for
// the window to close, and falls back to kill-window if it doesn't.
// Returns an error if the window could not be closed.
func (m *Manager) gracefulCloseWindow(task *db.Task) error {
	shellPID, err := tmux.PanePID(task.TmuxWindow)
	if err != nil {
		_ = m.events.Log(task.ID, "shutdown_warning", "could not get pane PID: "+err.Error())
		if err := tmux.KillWindow(task.TmuxWindow); err != nil {
			return fmt.Errorf("kill-window after PID lookup failure: %w", err)
		}
		if tmux.WindowExists(task.TmuxWindow) {
			return fmt.Errorf("window %s still exists after kill-window", task.TmuxWindow)
		}
		return nil
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
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}

	_ = m.events.Log(task.ID, "graceful_shutdown_timeout", "fell back to kill-window")
	if err := tmux.KillWindow(task.TmuxWindow); err != nil {
		_ = m.events.Log(task.ID, "kill_window_error", err.Error())
		return fmt.Errorf("kill-window: %w", err)
	}
	if tmux.WindowExists(task.TmuxWindow) {
		_ = m.events.Log(task.ID, "kill_window_error", "window still exists after kill-window")
		return fmt.Errorf("window %s still exists after kill-window", task.TmuxWindow)
	}
	return nil
}

// shutdownClaudeForRelaunch SIGINTs the Claude process, waits for it
// to exit (ensuring the session file is persisted), then kills the
// tmux window. Unlike gracefulCloseWindow, this waits for the process
// rather than the window, avoiding the race where kill-window
// interrupts Claude before it finishes saving.
func (m *Manager) shutdownClaudeForRelaunch(task *db.Task) {
	shellPID, err := tmux.PanePID(task.TmuxWindow)
	if err != nil {
		_ = m.events.Log(task.ID, "shutdown_warning", "could not get pane PID: "+err.Error())
		_ = tmux.KillWindow(task.TmuxWindow)
		return
	}

	claudePID := findClaudeChild(shellPID)
	if claudePID > 0 {
		_ = syscall.Kill(claudePID, syscall.SIGINT)

		// Wait for the Claude process to exit, not the window.
		deadline := time.Now().Add(gracefulShutdownTimeout)
		for time.Now().Before(deadline) {
			if err := syscall.Kill(claudePID, 0); err != nil {
				// Process gone — session is saved.
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
	}

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

// ForkTask creates a new task that forks from an existing task's Claude
// session. The new task starts with no session ID — Claude assigns one
// via SessionStart when it launches with --fork-session.
func (m *Manager) ForkTask(name, sourceSessionID, sourceTaskID, cwd string, flags db.TaskFlags, sandboxProfile, workspaceDir string) (*db.Task, error) {
	taskID := ulid.Make().String()

	task := &db.Task{
		ID:             taskID,
		Name:           name,
		State:          db.StateActive,
		Attention:      db.AttentionOK,
		Cwd:            cwd,
		Flags:          flags,
		SandboxProfile: sandboxProfile,
		WorkspaceDir:   workspaceDir,
		SourceTaskID:   sourceTaskID,
	}

	sandboxCmd := m.resolveSandboxCommand(sandboxProfile)
	claudeCmd := buildClaudeCommand("", name, flags, false, sandboxCmd, m.stateFilePath, m.templateData(name, cwd), sourceSessionID)

	windowName := tmux.WindowName(name)
	windowID, err := tmux.CreateWindow(m.activeSession, windowName, cwd, claudeCmd)
	if err != nil {
		return nil, fmt.Errorf("creating tmux window for fork: %w", err)
	}
	_ = tmux.SetWindowOption(windowID, "krang-task", name)
	task.TmuxWindow = windowID

	if err := m.tasks.Create(task); err != nil {
		tmux.KillWindow(windowID)
		return nil, fmt.Errorf("saving forked task: %w", err)
	}

	return task, nil
}

// CopySessionFiles copies a Claude session's JSONL file and optional
// companion directory from one project directory to another. This is
// needed when forking into a new workspace (different cwd) so Claude
// can find the session to fork from.
func CopySessionFiles(sessionID, oldCwd, newCwd string) error {
	if oldCwd == newCwd {
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	projectsDir := filepath.Join(home, ".claude", "projects")
	oldDir := filepath.Join(projectsDir, pathutil.EncodePath(oldCwd))
	newDir := filepath.Join(projectsDir, pathutil.EncodePath(newCwd))

	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return fmt.Errorf("creating new project dir: %w", err)
	}

	// Copy the session JSONL file.
	sessionFile := sessionID + ".jsonl"
	srcFile := filepath.Join(oldDir, sessionFile)
	dstFile := filepath.Join(newDir, sessionFile)

	data, err := os.ReadFile(srcFile)
	if err != nil {
		return fmt.Errorf("reading session file: %w", err)
	}
	if err := os.WriteFile(dstFile, data, 0o644); err != nil {
		return fmt.Errorf("writing session file: %w", err)
	}

	// Copy the companion directory if it exists.
	companionSrc := filepath.Join(oldDir, sessionID)
	if info, err := os.Stat(companionSrc); err == nil && info.IsDir() {
		companionDst := filepath.Join(newDir, sessionID)
		cpCmd := exec.Command("cp", "-a", companionSrc, companionDst)
		if output, err := cpCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("copying companion dir: %w: %s", err, output)
		}
	}

	return nil
}

// CleanupCopiedSession removes session files that were copied for
// forking. Called after the forked session has been adopted.
func CleanupCopiedSession(sessionID, cwd string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, ".claude", "projects", pathutil.EncodePath(cwd))

	// Remove the JSONL file.
	_ = os.Remove(filepath.Join(dir, sessionID+".jsonl"))

	// Remove the companion directory.
	_ = os.RemoveAll(filepath.Join(dir, sessionID))

	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
