//go:build integration

package integration

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// buildOnce caches the compiled krang and fakeclaude binaries across tests.
var buildOnce sync.Once
var krangBinPath string
var fakeClaudeBinPath string
var buildErr error

func buildBinaries(t *testing.T) {
	t.Helper()
	buildOnce.Do(func() {
		binDir, err := os.MkdirTemp("", "krang-integration-bins-*")
		if err != nil {
			buildErr = err
			return
		}

		moduleRoot := findModuleRoot()

		krangBinPath = filepath.Join(binDir, "krang")
		cmd := exec.Command("go", "build", "-o", krangBinPath, ".")
		cmd.Dir = moduleRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("building krang: %w: %s", err, out)
			return
		}

		fakeClaudeBinPath = filepath.Join(binDir, "fakeclaude")
		cmd = exec.Command("go", "build", "-o", fakeClaudeBinPath, "./internal/testutil/fakeclaude/")
		cmd.Dir = moduleRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("building fakeclaude: %w: %s", err, out)
			return
		}
	})
	if buildErr != nil {
		t.Fatalf("building binaries: %v", buildErr)
	}
}

func findModuleRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("could not find module root (go.mod)")
		}
		dir = parent
	}
}

// FakeClaudeManifest matches the JSON written by the fakeclaude binary.
type FakeClaudeManifest struct {
	PID         int       `json:"pid"`
	SessionID   string    `json:"session_id"`
	Name        string    `json:"name"`
	Resume      string    `json:"resume"`
	ForkSession bool      `json:"fork_session"`
	Cwd         string    `json:"cwd"`
	SkipPerms   bool      `json:"skip_permissions"`
	StartedAt   time.Time `json:"started_at"`
}

// TestEnv provides an isolated environment for a single integration test.
type TestEnv struct {
	t               *testing.T
	rootDir         string
	homeDir         string
	projectDir      string
	dbPath          string
	configPath      string
	fakeClaudeDir   string
	krangSession    string
	parkedSession   string
	krangPaneTarget string
	hookPort        int
	db              *sql.DB
}

type hookStateFile struct {
	Port int `json:"port"`
}

// NewTestEnv creates a fully isolated test environment with krang running
// in a detached tmux session. Cleanup is automatic via t.Cleanup.
func NewTestEnv(t *testing.T) *TestEnv {
	t.Helper()
	buildBinaries(t)

	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	projectDir := filepath.Join(root, "project")
	fakeClaudeDir := filepath.Join(root, "fakeclaude-control")
	dbPath := filepath.Join(root, "krang.db")
	configPath := filepath.Join(root, "config.yaml")

	for _, d := range []string{homeDir, projectDir, fakeClaudeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("creating dir %s: %v", d, err)
		}
	}

	// Write minimal config (no sandboxes, classification disabled).
	configContent := "theme: classic\nclassify_attention: false\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	// Compute the session names krang will use.
	iid := instanceID(projectDir)
	krangSession := "k-" + iid
	parkedSession := krangSession + "-parked"

	// Create a temporary tmux session with krang as the shell command.
	// This is more reliable than send-keys because there's no shell
	// initialization race.
	tempSession := fmt.Sprintf("krang-test-%d", time.Now().UnixNano())
	krangShellCmd := fmt.Sprintf(
		"env HOME=%s KRANG_DB=%s KRANG_CONFIG=%s KRANG_CLAUDE_CMD=%s FAKECLAUDE_CONTROLDIR=%s %s; sleep 999",
		shellQuote(homeDir),
		shellQuote(dbPath),
		shellQuote(configPath),
		shellQuote(fakeClaudeBinPath),
		shellQuote(fakeClaudeDir),
		shellQuote(krangBinPath),
	)

	cmd := exec.Command("tmux", "new-session", "-d", "-s", tempSession,
		"-x", "120", "-y", "40", "-c", projectDir,
		"sh", "-c", krangShellCmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("creating tmux session: %v: %s", err, out)
	}

	// Set env vars at both session and global level so child windows
	// (Claude/fakeclaude processes) created by krang inherit them.
	// Global env is needed because tmux new-window may not always
	// inherit session env on all platforms.
	for _, kv := range [][2]string{
		{"HOME", homeDir},
		{"KRANG_CLAUDE_CMD", fakeClaudeBinPath},
		{"FAKECLAUDE_CONTROLDIR", fakeClaudeDir},
	} {
		exec.Command("tmux", "set-environment", "-t", tempSession, kv[0], kv[1]).Run()
		exec.Command("tmux", "set-environment", "-g", kv[0], kv[1]).Run()
	}

	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", krangSession).Run()
		exec.Command("tmux", "kill-session", "-t", parkedSession).Run()
		exec.Command("tmux", "kill-session", "-t", tempSession).Run()
		// Clean up global env vars.
		for _, key := range []string{"HOME", "KRANG_CLAUDE_CMD", "FAKECLAUDE_CONTROLDIR"} {
			exec.Command("tmux", "set-environment", "-g", "-u", key).Run()
		}
	})

	env := &TestEnv{
		t:             t,
		rootDir:       root,
		homeDir:       homeDir,
		projectDir:    projectDir,
		dbPath:        dbPath,
		configPath:    configPath,
		fakeClaudeDir: fakeClaudeDir,
		krangSession:  krangSession,
		parkedSession: parkedSession,
	}

	// Wait for krang to start: the state file appears when the hook server is ready.
	stateFilePath := filepath.Join(homeDir, ".local", "state", "krang", "instances", encodePath(projectDir), "krang-state.json")
	env.WaitFor("krang state file", 15*time.Second, func() bool {
		_, err := os.Stat(stateFilePath)
		return err == nil
	})

	// Read the hook server port.
	data, err := os.ReadFile(stateFilePath)
	if err != nil {
		t.Fatalf("reading state file: %v", err)
	}
	var sf hookStateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("parsing state file: %v", err)
	}
	env.hookPort = sf.Port

	// Wait for the krang session to exist (after rename).
	env.WaitFor("krang session exists", 10*time.Second, func() bool {
		return exec.Command("tmux", "has-session", "-t", krangSession).Run() == nil
	})
	env.krangPaneTarget = krangSession + ":0.0"

	// Open the DB for assertions.
	database, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("opening test DB: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	env.db = database

	// Let krang fully initialize (reconcile, render first frame).
	time.Sleep(500 * time.Millisecond)

	return env
}

// SendKeys sends keystrokes to krang's TUI pane.
func (e *TestEnv) SendKeys(keys string) {
	e.t.Helper()
	cmd := exec.Command("tmux", "send-keys", "-t", e.krangPaneTarget, keys)
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("send-keys %q: %v: %s", keys, err, out)
	}
}

// SendHook POSTs a hook event to krang's HTTP server.
func (e *TestEnv) SendHook(event map[string]interface{}) {
	e.t.Helper()
	body, _ := json.Marshal(event)
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/hooks/event", e.hookPort),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		e.t.Fatalf("sending hook: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		e.t.Fatalf("hook server returned %d", resp.StatusCode)
	}
}

// WaitFor polls fn every 100ms until it returns true or timeout expires.
func (e *TestEnv) WaitFor(desc string, timeout time.Duration, fn func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Capture pane for debugging on failure (if target is set).
	var pane string
	if e.krangPaneTarget != "" {
		pane = e.CapturePane()
	}
	e.t.Fatalf("timed out waiting for: %s (after %v)\nPane content:\n%s", desc, timeout, pane)
}

// WaitForTaskState waits for a task to reach the given state.
func (e *TestEnv) WaitForTaskState(name string, state string) {
	e.t.Helper()
	e.WaitFor(fmt.Sprintf("task %q state=%s", name, state), 10*time.Second, func() bool {
		var s string
		err := e.db.QueryRow("SELECT state FROM tasks WHERE name = ?", name).Scan(&s)
		return err == nil && s == state
	})
}

// WaitForTaskAttention waits for a task to reach the given attention state.
func (e *TestEnv) WaitForTaskAttention(name string, attention string) {
	e.t.Helper()
	e.WaitFor(fmt.Sprintf("task %q attention=%s", name, attention), 10*time.Second, func() bool {
		var a string
		err := e.db.QueryRow("SELECT attention FROM tasks WHERE name = ?", name).Scan(&a)
		return err == nil && a == attention
	})
}

// WaitForEvent waits for a specific event type to appear in the events
// table for the named task. This is used to synchronize on hook event
// processing — the event is logged inside the handleHookEvent goroutine,
// so its presence confirms the goroutine has executed past that point.
func (e *TestEnv) WaitForEvent(taskName string, eventType string) {
	e.t.Helper()
	e.WaitForEventCount(taskName, eventType, 1)
}

// WaitForEventCount waits for at least n events of the given type.
func (e *TestEnv) WaitForEventCount(taskName string, eventType string, n int) {
	e.t.Helper()
	e.WaitFor(fmt.Sprintf("%d× event %s for task %q", n, eventType, taskName), 10*time.Second, func() bool {
		var count int
		e.db.QueryRow(`
			SELECT COUNT(*) FROM events e
			JOIN tasks t ON t.id = e.task_id
			WHERE t.name = ? AND e.event_type = ?`,
			taskName, eventType).Scan(&count)
		return count >= n
	})
}

// WaitForTaskExists waits for a task to appear in the DB.
func (e *TestEnv) WaitForTaskExists(name string) {
	e.t.Helper()
	e.WaitFor(fmt.Sprintf("task %q exists", name), 10*time.Second, func() bool {
		var count int
		e.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE name = ?", name).Scan(&count)
		return count > 0
	})
}

// TaskSessionID returns the session ID for a task.
func (e *TestEnv) TaskSessionID(name string) string {
	e.t.Helper()
	var sid string
	if err := e.db.QueryRow("SELECT session_id FROM tasks WHERE name = ?", name).Scan(&sid); err != nil {
		e.t.Fatalf("getting session_id for %q: %v", name, err)
	}
	return sid
}

// TaskTmuxWindow returns the tmux window ID for a task.
func (e *TestEnv) TaskTmuxWindow(name string) string {
	e.t.Helper()
	var wid sql.NullString
	if err := e.db.QueryRow("SELECT tmux_window FROM tasks WHERE name = ?", name).Scan(&wid); err != nil {
		e.t.Fatalf("getting tmux_window for %q: %v", name, err)
	}
	if wid.Valid {
		return wid.String
	}
	return ""
}

// TaskCwd returns the cwd for a task.
func (e *TestEnv) TaskCwd(name string) string {
	e.t.Helper()
	var cwd string
	if err := e.db.QueryRow("SELECT cwd FROM tasks WHERE name = ?", name).Scan(&cwd); err != nil {
		e.t.Fatalf("getting cwd for %q: %v", name, err)
	}
	return cwd
}

// TaskSourceID returns the source_task_id for a task.
func (e *TestEnv) TaskSourceID(name string) string {
	e.t.Helper()
	var sid sql.NullString
	if err := e.db.QueryRow("SELECT source_task_id FROM tasks WHERE name = ?", name).Scan(&sid); err != nil {
		e.t.Fatalf("getting source_task_id for %q: %v", name, err)
	}
	if sid.Valid {
		return sid.String
	}
	return ""
}

// TmuxWindowExists checks if a window with the given name exists in a session.
func (e *TestEnv) TmuxWindowExists(session, windowName string) bool {
	cmd := exec.Command("tmux", "list-windows", "-t", session, "-F", "#{window_name}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == windowName {
			return true
		}
	}
	return false
}

// CapturePane returns the current text content of krang's pane.
func (e *TestEnv) CapturePane() string {
	cmd := exec.Command("tmux", "capture-pane", "-t", e.krangPaneTarget, "-p")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// WaitForPaneContent polls until the pane contains the expected substring.
func (e *TestEnv) WaitForPaneContent(substring string) {
	e.t.Helper()
	e.WaitFor(fmt.Sprintf("pane contains %q", substring), 10*time.Second, func() bool {
		return strings.Contains(e.CapturePane(), substring)
	})
}

// WaitForPaneAbsent polls until the pane does NOT contain the substring.
func (e *TestEnv) WaitForPaneAbsent(substring string) {
	e.t.Helper()
	e.WaitFor(fmt.Sprintf("pane absent %q", substring), 10*time.Second, func() bool {
		return !strings.Contains(e.CapturePane(), substring)
	})
}

// WaitForPaneMatch polls until the pane matches the given regex.
func (e *TestEnv) WaitForPaneMatch(pattern *regexp.Regexp) {
	e.t.Helper()
	e.WaitFor(fmt.Sprintf("pane matches %s", pattern), 10*time.Second, func() bool {
		return pattern.MatchString(e.CapturePane())
	})
}

// PaneContains checks (non-blocking) if the pane currently contains text.
func (e *TestEnv) PaneContains(substring string) bool {
	return strings.Contains(e.CapturePane(), substring)
}

// FakeClaudeManifests returns all manifests written by fakeclaude instances.
func (e *TestEnv) FakeClaudeManifests() []FakeClaudeManifest {
	entries, err := os.ReadDir(e.fakeClaudeDir)
	if err != nil {
		return nil
	}
	var manifests []FakeClaudeManifest
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(e.fakeClaudeDir, entry.Name()))
		if err != nil {
			continue
		}
		var m FakeClaudeManifest
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		manifests = append(manifests, m)
	}
	return manifests
}

// LatestManifest returns the most recently started fakeclaude manifest.
func (e *TestEnv) LatestManifest() *FakeClaudeManifest {
	manifests := e.FakeClaudeManifests()
	if len(manifests) == 0 {
		return nil
	}
	latest := manifests[0]
	for _, m := range manifests[1:] {
		if m.StartedAt.After(latest.StartedAt) {
			latest = m
		}
	}
	return &latest
}

// WaitForManifestCount waits until the expected number of manifests exist.
func (e *TestEnv) WaitForManifestCount(count int) {
	e.t.Helper()
	e.WaitFor(fmt.Sprintf("%d fakeclaude manifest(s)", count), 10*time.Second, func() bool {
		return len(e.FakeClaudeManifests()) >= count
	})
}

// CreateTask drives the wizard to create a non-workspace task.
func (e *TestEnv) CreateTask(name string) {
	e.t.Helper()
	e.SendKeys("n")
	time.Sleep(400 * time.Millisecond)
	e.SendKeys(name)
	time.Sleep(200 * time.Millisecond)
	e.SendKeys("Enter")
	time.Sleep(300 * time.Millisecond)
	e.SendKeys("Enter")
	e.WaitForTaskExists(name)
	e.WaitForTaskState(name, "active")
}

// NewWorkspaceTestEnv creates a test environment with workspace mode enabled.
// It writes a krang.yaml with the given strategy and VCS type, then creates
// repos in the repos directory. Repos are minimal with a single initial commit.
// The vcs parameter should be "git" or "jj".
func NewWorkspaceTestEnv(t *testing.T, strategy, vcs string, repoNames []string) *TestEnv {
	t.Helper()
	buildBinaries(t)

	if vcs == "jj" {
		if _, err := exec.LookPath("jj"); err != nil {
			t.Skip("jj not installed")
		}
	}

	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	projectDir := filepath.Join(root, "project")
	fakeClaudeDir := filepath.Join(root, "fakeclaude-control")
	dbPath := filepath.Join(root, "krang.db")
	configPath := filepath.Join(root, "config.yaml")

	reposDir := filepath.Join(projectDir, "repos")
	workspacesDir := filepath.Join(projectDir, "workspaces")

	for _, d := range []string{homeDir, projectDir, fakeClaudeDir, reposDir, workspacesDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("creating dir %s: %v", d, err)
		}
	}

	// Write krang.yaml with VCS preference.
	krangYaml := fmt.Sprintf("workspace_strategy: %s\nrepos_dir: repos\nworkspaces_dir: workspaces\ndefault_vcs: %s\n", strategy, vcs)
	if err := os.WriteFile(filepath.Join(projectDir, "krang.yaml"), []byte(krangYaml), 0o644); err != nil {
		t.Fatalf("writing krang.yaml: %v", err)
	}

	// Create repos with the specified VCS.
	for _, name := range repoNames {
		repoPath := filepath.Join(reposDir, name)
		if err := os.MkdirAll(repoPath, 0o755); err != nil {
			t.Fatalf("creating repo dir %s: %v", repoPath, err)
		}
		switch vcs {
		case "jj":
			initJJRepoForIntegration(t, repoPath)
		default:
			initGitRepoForIntegration(t, repoPath)
		}
	}

	// Write minimal config.
	configContent := "theme: classic\nclassify_attention: false\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	iid := instanceID(projectDir)
	krangSession := "k-" + iid
	parkedSession := krangSession + "-parked"

	tempSession := fmt.Sprintf("krang-test-%d", time.Now().UnixNano())
	krangShellCmd := fmt.Sprintf(
		"env HOME=%s KRANG_DB=%s KRANG_CONFIG=%s KRANG_CLAUDE_CMD=%s FAKECLAUDE_CONTROLDIR=%s %s; sleep 999",
		shellQuote(homeDir),
		shellQuote(dbPath),
		shellQuote(configPath),
		shellQuote(fakeClaudeBinPath),
		shellQuote(fakeClaudeDir),
		shellQuote(krangBinPath),
	)

	cmd := exec.Command("tmux", "new-session", "-d", "-s", tempSession,
		"-x", "120", "-y", "40", "-c", projectDir,
		"sh", "-c", krangShellCmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("creating tmux session: %v: %s", err, out)
	}

	for _, kv := range [][2]string{
		{"HOME", homeDir},
		{"KRANG_CLAUDE_CMD", fakeClaudeBinPath},
		{"FAKECLAUDE_CONTROLDIR", fakeClaudeDir},
	} {
		exec.Command("tmux", "set-environment", "-t", tempSession, kv[0], kv[1]).Run()
		exec.Command("tmux", "set-environment", "-g", kv[0], kv[1]).Run()
	}

	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", krangSession).Run()
		exec.Command("tmux", "kill-session", "-t", parkedSession).Run()
		exec.Command("tmux", "kill-session", "-t", tempSession).Run()
		for _, key := range []string{"HOME", "KRANG_CLAUDE_CMD", "FAKECLAUDE_CONTROLDIR"} {
			exec.Command("tmux", "set-environment", "-g", "-u", key).Run()
		}
	})

	env := &TestEnv{
		t:             t,
		rootDir:       root,
		homeDir:       homeDir,
		projectDir:    projectDir,
		dbPath:        dbPath,
		configPath:    configPath,
		fakeClaudeDir: fakeClaudeDir,
		krangSession:  krangSession,
		parkedSession: parkedSession,
	}

	stateFilePath := filepath.Join(homeDir, ".local", "state", "krang", "instances", encodePath(projectDir), "krang-state.json")
	env.WaitFor("krang state file", 15*time.Second, func() bool {
		_, err := os.Stat(stateFilePath)
		return err == nil
	})

	data, err := os.ReadFile(stateFilePath)
	if err != nil {
		t.Fatalf("reading state file: %v", err)
	}
	var sf hookStateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("parsing state file: %v", err)
	}
	env.hookPort = sf.Port

	env.WaitFor("krang session exists", 10*time.Second, func() bool {
		return exec.Command("tmux", "has-session", "-t", krangSession).Run() == nil
	})
	env.krangPaneTarget = krangSession + ":0.0"

	database, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("opening test DB: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	env.db = database

	time.Sleep(500 * time.Millisecond)

	return env
}

// TaskWorkspaceDir returns the workspace_dir for a task.
func (e *TestEnv) TaskWorkspaceDir(name string) string {
	e.t.Helper()
	var dir sql.NullString
	if err := e.db.QueryRow("SELECT workspace_dir FROM tasks WHERE name = ?", name).Scan(&dir); err != nil {
		e.t.Fatalf("getting workspace_dir for %q: %v", name, err)
	}
	if dir.Valid {
		return dir.String
	}
	return ""
}

// CreateSingleRepoTask drives the wizard to create a single_repo workspace task,
// selecting the first repo in the list.
func (e *TestEnv) CreateSingleRepoTask(name string) {
	e.t.Helper()
	e.SendKeys("n")
	time.Sleep(400 * time.Millisecond)
	e.SendKeys(name)
	time.Sleep(200 * time.Millisecond)

	// Enter to advance from Name tab to Repos tab.
	e.SendKeys("Enter")
	time.Sleep(400 * time.Millisecond)

	// Single-repo shows a huh Select. Default is "(none — empty workspace)".
	// Navigate down to the first repo.
	e.SendKeys("j")
	time.Sleep(200 * time.Millisecond)

	// Submit.
	e.SendKeys("Enter")

	e.WaitForTaskExists(name)
	e.WaitForTaskState(name, "active")

	// Dismiss the workspace progress modal.
	e.WaitForPaneContent("Done!")
	e.SendKeys("Escape")
	time.Sleep(300 * time.Millisecond)
}

// CreateMultiRepoTask drives the wizard to create a multi_repo workspace task,
// toggling the first N repos.
func (e *TestEnv) CreateMultiRepoTask(name string, repoCount int) {
	e.t.Helper()
	e.SendKeys("n")
	time.Sleep(400 * time.Millisecond)
	e.SendKeys(name)
	time.Sleep(200 * time.Millisecond)

	// Enter to advance from Name tab to Repos tab.
	e.SendKeys("Enter")
	time.Sleep(400 * time.Millisecond)

	// Multi-repo shows a repo picker. Toggle repos with space, navigate with j.
	for i := 0; i < repoCount; i++ {
		if i > 0 {
			e.SendKeys("j")
			time.Sleep(100 * time.Millisecond)
		}
		e.SendKeys(" ")
		time.Sleep(100 * time.Millisecond)
	}

	// Submit.
	e.SendKeys("Enter")

	e.WaitForTaskExists(name)
	e.WaitForTaskState(name, "active")

	// Dismiss the workspace progress modal.
	e.WaitForPaneContent("Done!")
	e.SendKeys("Escape")
	time.Sleep(300 * time.Millisecond)
}

// ReposDir returns the repos directory path for workspace test environments.
func (e *TestEnv) ReposDir() string {
	return filepath.Join(e.projectDir, "repos")
}

// WorkspacesDir returns the workspaces directory path.
func (e *TestEnv) WorkspacesDir() string {
	return filepath.Join(e.projectDir, "workspaces")
}

// --- helper functions ---

func initGitRepoForIntegration(t *testing.T, repoPath string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoPath
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v: %s", args, repoPath, err, out)
		}
	}
}

func initJJRepoForIntegration(t *testing.T, repoPath string) {
	t.Helper()
	for _, args := range [][]string{
		{"git", "init"},
		{"describe", "-m", "initial"},
		{"new"},
	} {
		cmd := exec.Command("jj", args...)
		cmd.Dir = repoPath
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("jj %v in %s: %v: %s", args, repoPath, err, out)
		}
	}
}

// gitBranchExists checks if a branch exists in a git repo.
func gitBranchExists(repoDir, branchName string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", branchName)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}

// gitWorktreeList returns the output of git worktree list for a repo.
func gitWorktreeList(t *testing.T, repoDir string) string {
	t.Helper()
	cmd := exec.Command("git", "worktree", "list")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list in %s: %v: %s", repoDir, err, out)
	}
	return string(out)
}

// jjWorkspaceList returns the output of jj workspace list for a repo.
func jjWorkspaceList(t *testing.T, repoDir string) string {
	t.Helper()
	cmd := exec.Command("jj", "workspace", "list")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("jj workspace list in %s: %v: %s", repoDir, err, out)
	}
	return string(out)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func encodePath(path string) string {
	var b strings.Builder
	for _, r := range path {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func instanceID(cwd string) string {
	basename := filepath.Base(cwd)
	hash := sha256.Sum256([]byte(cwd))
	return fmt.Sprintf("%s-%x", basename, hash[:2])
}
