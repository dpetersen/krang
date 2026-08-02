package task

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dpetersen/krang/internal/config"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/pathutil"
)

const testStateFile = "/tmp/krang-state.json"
const statePrefix = "export KRANG_STATEFILE='/tmp/krang-state.json'; "

func TestBuildClaudeCommandDefaults(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, false, "safehouse", testStateFile, sandboxTemplateData{}, "")
	expected := statePrefix + "safehouse claude --session-id sess-123 --name 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandResume(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, true, "safehouse", testStateFile, sandboxTemplateData{}, "")
	expected := statePrefix + "safehouse claude --resume 'sess-123'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandEmptySandbox(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, false, "", testStateFile, sandboxTemplateData{}, "")
	expected := statePrefix + "claude --session-id sess-123 --name 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandSkipPermissions(t *testing.T) {
	flags := db.TaskFlags{DangerouslySkipPermissions: true}
	cmd := buildClaudeCommand("sess-123", "my-task", flags, false, "safehouse", testStateFile, sandboxTemplateData{}, "")
	expected := statePrefix + "safehouse claude --session-id sess-123 --name 'my-task' --dangerously-skip-permissions; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandAllFlags(t *testing.T) {
	flags := db.TaskFlags{DangerouslySkipPermissions: true}
	cmd := buildClaudeCommand("sess-123", "my-task", flags, true, "", testStateFile, sandboxTemplateData{}, "")
	expected := statePrefix + "claude --resume 'sess-123' --dangerously-skip-permissions; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandResumeUsesSessionID(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, true, "safehouse", testStateFile, sandboxTemplateData{}, "")
	if expected := "safehouse claude --resume 'sess-123'"; !contains(cmd, expected) {
		t.Errorf("resume command should use session ID:\n  %s", cmd)
	}
	if contains(cmd, "my-task") {
		t.Errorf("resume command should not contain task name:\n  %s", cmd)
	}
}

func TestBuildClaudeCommandCustomSandbox(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, false, "safehouse --append-profile ~/.config/safehouse/allow-nah.sb", testStateFile, sandboxTemplateData{}, "")
	expected := statePrefix + "safehouse --append-profile ~/.config/safehouse/allow-nah.sb claude --session-id sess-123 --name 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandNoStateFile(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, false, "safehouse", "", sandboxTemplateData{}, "")
	expected := "safehouse claude --session-id sess-123 --name 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandTemplateExpansion(t *testing.T) {
	tmplData := sandboxTemplateData{
		KrangDir: "/home/user/code/project",
		TaskCwd:  "/home/user/code/project/workspaces/fix-auth",
		TaskName: "fix-auth",
		ReposDir: "/home/user/code/project/repos",
	}
	sandbox := "safehouse --add-dirs-ro={{.KrangDir}}/.mcp.json:{{.KrangDir}}/CLAUDE.md:{{.KrangDir}}/.claude"
	cmd := buildClaudeCommand("sess-123", "fix-auth", db.TaskFlags{}, false, sandbox, testStateFile, tmplData, "")
	expectedSandbox := "safehouse --add-dirs-ro=/home/user/code/project/.mcp.json:/home/user/code/project/CLAUDE.md:/home/user/code/project/.claude"
	if !contains(cmd, expectedSandbox) {
		t.Errorf("template not expanded:\n  %s", cmd)
	}
}

func TestExpandSandboxCommandNoTemplate(t *testing.T) {
	result := expandSandboxCommand("safehouse", sandboxTemplateData{KrangDir: "/foo"})
	if result != "safehouse" {
		t.Errorf("plain string should pass through unchanged, got: %s", result)
	}
}

func TestExpandSandboxCommandAllVars(t *testing.T) {
	tmplData := sandboxTemplateData{
		KrangDir: "/code",
		TaskCwd:  "/code/workspaces/task1",
		TaskName: "task1",
		ReposDir: "/code/repos",
	}
	result := expandSandboxCommand("safehouse --ro={{.KrangDir}} --cwd={{.TaskCwd}} --name={{.TaskName}} --repos={{.ReposDir}}", tmplData)
	expected := "safehouse --ro=/code --cwd=/code/workspaces/task1 --name=task1 --repos=/code/repos"
	if result != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, result)
	}
}

func TestResolveSandboxCommandExplicitProfile(t *testing.T) {
	m := &Manager{
		sandboxProfiles: map[string]config.SandboxProfile{
			"default": {Type: "command", Command: "safehouse run"},
			"cloud":   {Type: "command", Command: "safehouse run --cloud"},
		},
		defaultSandbox: "default",
	}
	if got := m.resolveSandboxCommand("cloud"); got != "safehouse run --cloud" {
		t.Errorf("expected cloud command, got %q", got)
	}
}

func TestResolveSandboxCommandDefault(t *testing.T) {
	m := &Manager{
		sandboxProfiles: map[string]config.SandboxProfile{
			"default": {Type: "command", Command: "safehouse run"},
		},
		defaultSandbox: "default",
	}
	if got := m.resolveSandboxCommand(""); got != "safehouse run" {
		t.Errorf("expected default command, got %q", got)
	}
}

func TestResolveSandboxCommandMissing(t *testing.T) {
	m := &Manager{
		sandboxProfiles: map[string]config.SandboxProfile{
			"default": {Type: "command", Command: "safehouse run"},
		},
		defaultSandbox: "default",
	}
	if got := m.resolveSandboxCommand("nonexistent"); got != "" {
		t.Errorf("expected empty for missing profile, got %q", got)
	}
}

func TestResolveSandboxCommandNone(t *testing.T) {
	m := &Manager{
		sandboxProfiles: map[string]config.SandboxProfile{
			"default": {Type: "command", Command: "safehouse run"},
		},
		defaultSandbox: "default",
	}
	// "none" is not a real profile — resolves to empty.
	if got := m.resolveSandboxCommand("none"); got != "" {
		t.Errorf("expected empty for 'none', got %q", got)
	}
}

func TestResolveSandboxCommandNoProfiles(t *testing.T) {
	m := &Manager{}
	if got := m.resolveSandboxCommand(""); got != "" {
		t.Errorf("expected empty with no profiles, got %q", got)
	}
}

func TestBuildClaudeCommandFork(t *testing.T) {
	cmd := buildClaudeCommand("", "fork-task", db.TaskFlags{}, false, "safehouse", testStateFile, sandboxTemplateData{}, "source-sess-id")
	expected := statePrefix + "safehouse claude --resume 'source-sess-id' --fork-session --name 'fork-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandForkNoSandbox(t *testing.T) {
	cmd := buildClaudeCommand("", "fork-task", db.TaskFlags{}, false, "", testStateFile, sandboxTemplateData{}, "source-sess-id")
	expected := statePrefix + "claude --resume 'source-sess-id' --fork-session --name 'fork-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandForkWithFlags(t *testing.T) {
	flags := db.TaskFlags{DangerouslySkipPermissions: true, Debug: true}
	cmd := buildClaudeCommand("", "fork-task", flags, false, "safehouse", testStateFile, sandboxTemplateData{}, "source-sess-id")
	if !contains(cmd, "--resume 'source-sess-id' --fork-session --name 'fork-task'") {
		t.Errorf("fork command missing --resume --fork-session --name:\n  %s", cmd)
	}
	if !contains(cmd, "--dangerously-skip-permissions") {
		t.Errorf("fork command missing --dangerously-skip-permissions:\n  %s", cmd)
	}
	if !contains(cmd, "KRANG_DEBUG=1") {
		t.Errorf("fork command missing KRANG_DEBUG:\n  %s", cmd)
	}
}

func TestBuildClaudeCommandCustomBinary(t *testing.T) {
	t.Setenv("KRANG_CLAUDE_CMD", "/tmp/fakeclaude")
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, false, "", testStateFile, sandboxTemplateData{}, "")
	expected := statePrefix + "/tmp/fakeclaude --session-id sess-123 --name 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandCustomBinaryWithSandbox(t *testing.T) {
	t.Setenv("KRANG_CLAUDE_CMD", "/tmp/fakeclaude")
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, false, "safehouse", testStateFile, sandboxTemplateData{}, "")
	expected := statePrefix + "safehouse /tmp/fakeclaude --session-id sess-123 --name 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestCopySessionFiles(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	oldCwd := "/tmp/old-project"
	newCwd := "/tmp/new-project"
	sessionID := "test-session-abc"

	// Create the source session file.
	oldDir := filepath.Join(homeDir, ".claude", "projects", pathutil.EncodePath(oldCwd))
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionContent := []byte(`{"type":"init"}` + "\n")
	if err := os.WriteFile(filepath.Join(oldDir, sessionID+".jsonl"), sessionContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a companion directory.
	companionDir := filepath.Join(oldDir, sessionID)
	if err := os.MkdirAll(companionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(companionDir, "attachment.png"), []byte("fake-image"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopySessionFiles(sessionID, oldCwd, newCwd); err != nil {
		t.Fatalf("CopySessionFiles: %v", err)
	}

	// Verify session file was copied.
	newDir := filepath.Join(homeDir, ".claude", "projects", pathutil.EncodePath(newCwd))
	data, err := os.ReadFile(filepath.Join(newDir, sessionID+".jsonl"))
	if err != nil {
		t.Fatalf("session file not copied: %v", err)
	}
	if string(data) != string(sessionContent) {
		t.Errorf("session file content = %q, want %q", string(data), string(sessionContent))
	}

	// Verify companion directory was copied.
	attachmentData, err := os.ReadFile(filepath.Join(newDir, sessionID, "attachment.png"))
	if err != nil {
		t.Fatalf("companion dir not copied: %v", err)
	}
	if string(attachmentData) != "fake-image" {
		t.Error("companion file content mismatch")
	}
}

func TestCopySessionFilesSameCwd(t *testing.T) {
	// Copying to the same cwd should be a no-op.
	if err := CopySessionFiles("session-id", "/same/path", "/same/path"); err != nil {
		t.Fatalf("CopySessionFiles same cwd: %v", err)
	}
}

func TestCleanupCopiedSession(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	cwd := "/tmp/fork-project"
	sessionID := "cleanup-session-xyz"

	// Set up files to clean.
	dir := filepath.Join(homeDir, ".claude", "projects", pathutil.EncodePath(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	companionDir := filepath.Join(dir, sessionID)
	if err := os.MkdirAll(companionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(companionDir, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CleanupCopiedSession(sessionID, "/tmp/source-project", cwd); err != nil {
		t.Fatalf("CleanupCopiedSession: %v", err)
	}

	// JSONL file should be gone.
	if _, err := os.Stat(filepath.Join(dir, sessionID+".jsonl")); !os.IsNotExist(err) {
		t.Error("session JSONL file should be removed")
	}

	// Companion directory should be gone.
	if _, err := os.Stat(companionDir); !os.IsNotExist(err) {
		t.Error("companion directory should be removed")
	}
}

func TestCleanupCopiedSessionSameCwd(t *testing.T) {
	// When oldCwd == newCwd, no copy was made, so cleanup must be a
	// no-op — otherwise it would delete the source's real session file.
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	cwd := "/tmp/shared-project"
	sessionID := "shared-session"

	dir := filepath.Join(homeDir, ".claude", "projects", pathutil.EncodePath(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(sessionFile, []byte("real-data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CleanupCopiedSession(sessionID, cwd, cwd); err != nil {
		t.Fatalf("CleanupCopiedSession: %v", err)
	}

	if _, err := os.Stat(sessionFile); err != nil {
		t.Fatalf("session file should remain for shared-workspace fork: %v", err)
	}
}

func TestFindSessionCwdPreferredHit(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	preferred := filepath.Join(homeDir, "real-workspace")
	if err := os.MkdirAll(preferred, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "prefer-session"
	projectDir := filepath.Join(homeDir, ".claude", "projects", pathutil.EncodePath(preferred))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, sessionID+".jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A stale copy in a lex-earlier project dir that decodes to a
	// nonexistent path — this is the exact production failure mode.
	staleDir := filepath.Join(homeDir, ".claude", "projects", pathutil.EncodePath("/aaa/stale/path"))
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, sessionID+".jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findSessionCwd(sessionID, preferred)
	if err != nil {
		t.Fatalf("findSessionCwd: %v", err)
	}
	if got != preferred {
		t.Errorf("findSessionCwd = %q, want preferred %q", got, preferred)
	}
}

func TestFindSessionCwdPrefersExistingOverStale(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	// Real workspace exists on disk. Use a single-segment name with
	// no hyphens so the decoder's naive fallback reconstructs it
	// correctly even on macOS where /var-via-symlink defeats the
	// filesystem walker.
	realCwd := filepath.Join(homeDir, "zzzreal")
	if err := os.MkdirAll(realCwd, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "pick-existing"

	realProject := filepath.Join(homeDir, ".claude", "projects", pathutil.EncodePath(realCwd))
	if err := os.MkdirAll(realProject, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realProject, sessionID+".jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Stale project dir: sorts before real (alphabetically), decodes
	// to a path that doesn't exist on disk.
	staleProject := filepath.Join(homeDir, ".claude", "projects", pathutil.EncodePath("/aaa/dead/workspace"))
	if err := os.MkdirAll(staleProject, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleProject, sessionID+".jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findSessionCwd(sessionID, "")
	if err != nil {
		t.Fatalf("findSessionCwd: %v", err)
	}
	if got != realCwd {
		t.Errorf("findSessionCwd = %q, want existing %q", got, realCwd)
	}
}

// Completing frees a task's name for reuse, so its recorded VCS
// identities have to be released at the same time — the workspace_repos
// unique constraint would otherwise reject the next task of that name.
func TestCompleteDropsRecordedWorkspaceRepos(t *testing.T) {
	f := newManagerFixture(t)

	makeRepoDir(t, filepath.Join(f.reposDir, "alpha"), "jj")
	workspaceDir := filepath.Join(f.workspacesDir, "finished")
	makeRepoDir(t, filepath.Join(workspaceDir, "alpha"), "jj")

	if err := f.tasks.Create(&db.Task{
		ID: "01DONE", Name: "finished", State: db.StateActive,
		Attention: db.AttentionOK, Cwd: workspaceDir, WorkspaceDir: workspaceDir,
	}); err != nil {
		t.Fatalf("creating task: %v", err)
	}
	if err := f.manager.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if err := f.manager.Complete("01DONE"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	rows, err := f.workspaceRepos.ListByTask("01DONE")
	if err != nil {
		t.Fatalf("listing workspace repos: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows after completing, want 0: %+v", len(rows), rows)
	}

	// The freed name can be taken by a new task holding the same repo.
	if err := f.workspaceRepos.Create(&db.WorkspaceRepo{
		TaskID: "01DONE", RepoName: "alpha", DirName: "alpha", VCS: "jj", VCSName: "finished",
	}); err != nil {
		t.Errorf("re-recording the freed identity: %v", err)
	}
}

// AC: completion releases EVERY recorded identity, not one per repo.
// Two checkouts of one repo are two rows, and both have to go or the
// next task of that name collides on the surviving one.
func TestCompleteReleasesEveryRecordedSlotIdentity(t *testing.T) {
	f := newManagerFixture(t)

	makeRepoDir(t, filepath.Join(f.reposDir, "alpha"), "jj")
	workspaceDir := filepath.Join(f.workspacesDir, "slotty")
	for _, dir := range []string{"alpha", "alpha--tests"} {
		makeRepoDir(t, filepath.Join(workspaceDir, dir), "jj")
	}

	if err := f.tasks.Create(&db.Task{
		ID: "01SLOTS", Name: "slotty", State: db.StateActive,
		Attention: db.AttentionOK, Cwd: workspaceDir, WorkspaceDir: workspaceDir,
	}); err != nil {
		t.Fatalf("creating task: %v", err)
	}
	for _, row := range []db.WorkspaceRepo{
		{TaskID: "01SLOTS", RepoName: "alpha", DirName: "alpha", VCS: "jj", VCSName: "slotty"},
		{TaskID: "01SLOTS", RepoName: "alpha", DirName: "alpha--tests", VCS: "jj",
			VCSName: "slotty--alpha--tests", SlotLabel: "tests"},
	} {
		if err := f.workspaceRepos.Create(&row); err != nil {
			t.Fatalf("recording %s: %v", row.DirName, err)
		}
	}

	if err := f.manager.Complete("01SLOTS"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	rows, err := f.workspaceRepos.ListByTask("01SLOTS")
	if err != nil {
		t.Fatalf("listing workspace repos: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows after completing, want 0: %+v", len(rows), rows)
	}

	// Both identities are free again, including the slot's.
	for _, row := range []db.WorkspaceRepo{
		{TaskID: "01SLOTS", RepoName: "alpha", DirName: "alpha", VCS: "jj", VCSName: "slotty"},
		{TaskID: "01SLOTS", RepoName: "alpha", DirName: "alpha--tests", VCS: "jj",
			VCSName: "slotty--alpha--tests", SlotLabel: "tests"},
	} {
		if err := f.workspaceRepos.Create(&row); err != nil {
			t.Errorf("re-recording %s: %v", row.VCSName, err)
		}
	}
}

// A workspace another task still shares is not destroyed, so its
// provenance must not be destroyed either: the surviving task is the one
// that will eventually tear the directory down, and without the rows it
// would have nothing to say which slots to forget.
func TestCompleteHandsProvenanceToASurvivingWorkspaceSharer(t *testing.T) {
	f := newManagerFixture(t)

	makeRepoDir(t, filepath.Join(f.reposDir, "alpha"), "jj")
	workspaceDir := filepath.Join(f.workspacesDir, "owner")
	for _, dir := range []string{"alpha", "alpha--tests"} {
		makeRepoDir(t, filepath.Join(workspaceDir, dir), "jj")
	}

	for _, task := range []db.Task{
		{ID: "01OWNER", Name: "owner", State: db.StateActive, Attention: db.AttentionOK,
			Cwd: workspaceDir, WorkspaceDir: workspaceDir},
		{ID: "01FORK", Name: "owner-fork", State: db.StateActive, Attention: db.AttentionOK,
			Cwd: workspaceDir, WorkspaceDir: workspaceDir, SourceTaskID: "01OWNER"},
	} {
		if err := f.tasks.Create(&task); err != nil {
			t.Fatalf("creating %s: %v", task.Name, err)
		}
	}
	for _, row := range []db.WorkspaceRepo{
		{TaskID: "01OWNER", RepoName: "alpha", DirName: "alpha", VCS: "jj", VCSName: "owner"},
		{TaskID: "01OWNER", RepoName: "alpha", DirName: "alpha--tests", VCS: "jj",
			VCSName: "owner--alpha--tests", SlotLabel: "tests"},
	} {
		if err := f.workspaceRepos.Create(&row); err != nil {
			t.Fatalf("recording %s: %v", row.DirName, err)
		}
	}

	if err := f.manager.Complete("01OWNER"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if rows, err := f.workspaceRepos.ListByTask("01OWNER"); err != nil {
		t.Fatalf("listing the completed task's rows: %v", err)
	} else if len(rows) != 0 {
		t.Errorf("the completed task kept %d rows: %+v", len(rows), rows)
	}

	rows, err := f.workspaceRepos.ListByTask("01FORK")
	if err != nil {
		t.Fatalf("listing the survivor's rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("the survivor holds %d rows, want both: %+v", len(rows), rows)
	}
	if rows[0].VCSName != "owner" || rows[1].VCSName != "owner--alpha--tests" {
		t.Errorf("rows = %+v, want the owner's identities intact", rows)
	}
}

// AC: freezing is not a teardown. It closes a window; the working
// copies, their VCS identities, and the rows recording them all stay
// exactly where the task left them, because unfreezing has to find them.
func TestFreezingLeavesWorkingCopiesAndTheirRowsAlone(t *testing.T) {
	f := newManagerFixture(t)

	makeRepoDir(t, filepath.Join(f.reposDir, "alpha"), "jj")
	workspaceDir := filepath.Join(f.workspacesDir, "chilly")
	for _, dir := range []string{"alpha", "alpha--tests"} {
		makeRepoDir(t, filepath.Join(workspaceDir, dir), "jj")
	}

	if err := f.tasks.Create(&db.Task{
		ID: "01FREEZE", Name: "chilly", State: db.StateActive, Attention: db.AttentionOK,
		SessionID: "session-1", Cwd: workspaceDir, WorkspaceDir: workspaceDir,
	}); err != nil {
		t.Fatalf("creating task: %v", err)
	}
	for _, row := range []db.WorkspaceRepo{
		{TaskID: "01FREEZE", RepoName: "alpha", DirName: "alpha", VCS: "jj", VCSName: "chilly"},
		{TaskID: "01FREEZE", RepoName: "alpha", DirName: "alpha--tests", VCS: "jj",
			VCSName: "chilly--alpha--tests", SlotLabel: "tests"},
	} {
		if err := f.workspaceRepos.Create(&row); err != nil {
			t.Fatalf("recording %s: %v", row.DirName, err)
		}
	}

	if err := f.manager.Dormify("01FREEZE"); err != nil {
		t.Fatalf("Dormify: %v", err)
	}

	rows, err := f.workspaceRepos.ListByTask("01FREEZE")
	if err != nil {
		t.Fatalf("listing workspace repos: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("freezing left %d rows, want both: %+v", len(rows), rows)
	}
	for _, dir := range []string{"alpha", "alpha--tests"} {
		if _, err := os.Stat(filepath.Join(workspaceDir, dir)); err != nil {
			t.Errorf("freezing removed %s: %v", dir, err)
		}
	}
}

// The reconciler's "failed" is a diagnosis, not a teardown: it leaves
// the rows alone deliberately, because the working copies are still
// there. Completing the task later is what releases them.
func TestCompletingAFailedTaskStillReleasesItsIdentities(t *testing.T) {
	f := newManagerFixture(t)

	makeRepoDir(t, filepath.Join(f.reposDir, "alpha"), "jj")
	workspaceDir := filepath.Join(f.workspacesDir, "crashed")
	makeRepoDir(t, filepath.Join(workspaceDir, "alpha"), "jj")

	if err := f.tasks.Create(&db.Task{
		ID: "01CRASH", Name: "crashed", State: db.StateActive, Attention: db.AttentionOK,
		Cwd: workspaceDir, WorkspaceDir: workspaceDir,
		// A window tmux does not have, which is what the reconciler
		// notices. No session ID, so the verdict is failed, not dormant.
		TmuxWindow: "@99999",
	}); err != nil {
		t.Fatalf("creating task: %v", err)
	}
	if err := f.workspaceRepos.Create(&db.WorkspaceRepo{
		TaskID: "01CRASH", RepoName: "alpha", DirName: "alpha", VCS: "jj", VCSName: "crashed",
	}); err != nil {
		t.Fatalf("recording the working copy: %v", err)
	}

	if err := f.manager.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	failed, err := f.tasks.Get("01CRASH")
	if err != nil || failed == nil {
		t.Fatalf("loading the reconciled task: %v", err)
	}
	if failed.State != db.StateFailed {
		t.Fatalf("state = %q, want failed", failed.State)
	}
	if rows, err := f.workspaceRepos.ListByTask("01CRASH"); err != nil || len(rows) != 1 {
		t.Fatalf("reconcile changed the rows (%+v, %v); marking failed must not release identities", rows, err)
	}

	if err := f.manager.Complete("01CRASH"); err != nil {
		t.Fatalf("Complete on a failed task: %v", err)
	}
	if rows, err := f.workspaceRepos.ListByTask("01CRASH"); err != nil || len(rows) != 0 {
		t.Errorf("rows after completing the failed task = %+v (%v), want none", rows, err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
