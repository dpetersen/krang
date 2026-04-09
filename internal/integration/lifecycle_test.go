//go:build integration

package integration

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateAndHookEvents(t *testing.T) {
	env := NewTestEnv(t)

	// Create a task via the wizard.
	env.CreateTask("hook-test")

	// Verify it appears in the TUI.
	env.WaitForPaneContent("hook-test")

	// Verify tmux window was created.
	if !env.TmuxWindowExists(env.krangSession, "hook-test") {
		t.Error("expected tmux window 'hook-test' in krang session")
	}

	// Verify fakeclaude was launched with correct flags.
	env.WaitForManifestCount(1)
	manifest := env.LatestManifest()
	if manifest == nil {
		t.Fatal("no fakeclaude manifest found")
	}
	if manifest.SessionID == "" {
		t.Error("fakeclaude should have a session ID")
	}
	if manifest.Name != "hook-test" {
		t.Errorf("fakeclaude name = %q, want %q", manifest.Name, "hook-test")
	}

	// No subdirectories → CWD should default to the project root.
	if taskCwd := env.TaskCwd("hook-test"); taskCwd != env.projectDir {
		t.Errorf("task cwd = %q, want %q", taskCwd, env.projectDir)
	}
	if manifest.Cwd != env.projectDir {
		t.Errorf("fakeclaude cwd = %q, want %q", manifest.Cwd, env.projectDir)
	}

	sessionID := env.TaskSessionID("hook-test")

	// Send SessionStart hook.
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "SessionStart",
		"cwd":             env.projectDir,
	})
	env.WaitForTaskAttention("hook-test", "ok")

	// Send tool use hooks.
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "PostToolUse",
		"cwd":             env.projectDir,
		"tool_name":       "Read",
	})
	time.Sleep(200 * time.Millisecond)
	env.WaitForTaskAttention("hook-test", "ok")

	// Send Stop -> attention becomes waiting.
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "Stop",
		"cwd":             env.projectDir,
	})
	env.WaitForTaskAttention("hook-test", "waiting")
	env.WaitForPaneContent("wait")

	// Send PermissionRequest -> attention becomes permission.
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "PermissionRequest",
		"cwd":             env.projectDir,
	})
	env.WaitForTaskAttention("hook-test", "permission")
	env.WaitForPaneContent("PERM")

	// Send UserPromptSubmit -> back to ok.
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "UserPromptSubmit",
		"cwd":             env.projectDir,
	})
	env.WaitForTaskAttention("hook-test", "ok")
	env.WaitForPaneContent("ok")
}

func TestSubagentPermissionNotClobbered(t *testing.T) {
	env := NewTestEnv(t)

	env.CreateTask("perm-test")
	sessionID := env.TaskSessionID("perm-test")

	// Start the session.
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "SessionStart",
		"cwd":             env.projectDir,
	})
	env.WaitForTaskAttention("perm-test", "ok")

	// Two subagents start.
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "SubagentStart",
		"cwd":             env.projectDir,
		"agent_id":        "agent-a",
		"agent_type":      "Explore",
	})
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "SubagentStart",
		"cwd":             env.projectDir,
		"agent_id":        "agent-b",
		"agent_type":      "Explore",
	})
	time.Sleep(200 * time.Millisecond)

	// Agent A hits a permission wall.
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "PermissionRequest",
		"cwd":             env.projectDir,
		"tool_name":       "Bash",
		"agent_id":        "agent-a",
	})
	env.WaitForTaskAttention("perm-test", "permission")

	// Agent B completes a tool — this must NOT clear the permission.
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "PostToolUse",
		"cwd":             env.projectDir,
		"tool_name":       "Read",
		"agent_id":        "agent-b",
	})
	time.Sleep(300 * time.Millisecond)
	env.WaitForTaskAttention("perm-test", "permission")

	// Agent A's permission is resolved (PostToolUse from same agent).
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "PostToolUse",
		"cwd":             env.projectDir,
		"tool_name":       "Bash",
		"agent_id":        "agent-a",
	})
	env.WaitForTaskAttention("perm-test", "ok")
}

func TestSubagentPermissionClearedByUserPrompt(t *testing.T) {
	env := NewTestEnv(t)

	env.CreateTask("perm-esc-test")
	sessionID := env.TaskSessionID("perm-esc-test")

	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "SessionStart",
		"cwd":             env.projectDir,
	})
	env.WaitForTaskAttention("perm-esc-test", "ok")

	// Subagent starts and hits a permission wall.
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "SubagentStart",
		"cwd":             env.projectDir,
		"agent_id":        "agent-a",
		"agent_type":      "Explore",
	})
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "PermissionRequest",
		"cwd":             env.projectDir,
		"tool_name":       "Bash",
		"agent_id":        "agent-a",
	})
	env.WaitForTaskAttention("perm-esc-test", "permission")

	// User hits escape and types something (UserPromptSubmit clears
	// pending permissions so we don't get stuck red forever).
	env.SendHook(map[string]interface{}{
		"session_id":      sessionID,
		"hook_event_name": "UserPromptSubmit",
		"cwd":             env.projectDir,
	})
	env.WaitForTaskAttention("perm-esc-test", "ok")
}

func TestParkUnpark(t *testing.T) {
	env := NewTestEnv(t)

	env.CreateTask("park-test")

	// Open detail modal and park.
	env.SendKeys("Tab")
	time.Sleep(300 * time.Millisecond)
	env.SendKeys("p")
	env.WaitForTaskState("park-test", "parked")

	// Window should have moved to parked session.
	env.WaitFor("window in parked session", 5*time.Second, func() bool {
		return env.TmuxWindowExists(env.parkedSession, "park-test")
	})

	// Close detail modal, re-open it, then unpark.
	env.SendKeys("Escape")
	time.Sleep(200 * time.Millisecond)
	env.SendKeys("Tab")
	time.Sleep(300 * time.Millisecond)
	env.SendKeys("p")
	env.WaitForTaskState("park-test", "active")

	// Window should be back in the krang session.
	env.WaitFor("window back in krang session", 5*time.Second, func() bool {
		return env.TmuxWindowExists(env.krangSession, "park-test")
	})
}

func TestFreezeUnfreeze(t *testing.T) {
	env := NewTestEnv(t)

	env.CreateTask("freeze-test")

	// Open detail modal and freeze.
	env.SendKeys("Tab")
	time.Sleep(300 * time.Millisecond)
	env.SendKeys("f")

	// Freeze involves SIGINT + 15s timeout, then kill-window.
	// DB state is updated after the window kill succeeds.
	env.WaitFor("tmux_window cleared after freeze", 25*time.Second, func() bool {
		return env.TaskTmuxWindow("freeze-test") == ""
	})

	env.WaitForTaskState("freeze-test", "dormant")

	// Window should be gone.
	if env.TmuxWindowExists(env.krangSession, "freeze-test") ||
		env.TmuxWindowExists(env.parkedSession, "freeze-test") {
		t.Error("window should be gone after freeze")
	}

	// Close and re-open detail modal to unfreeze.
	env.SendKeys("Escape")
	time.Sleep(200 * time.Millisecond)
	env.SendKeys("Tab")
	time.Sleep(300 * time.Millisecond)
	env.SendKeys("f")
	env.WaitForTaskState("freeze-test", "active")

	// New window should exist.
	env.WaitFor("window recreated after unfreeze", 10*time.Second, func() bool {
		return env.TmuxWindowExists(env.krangSession, "freeze-test")
	})

	// Verify fakeclaude was relaunched with --resume.
	env.WaitForManifestCount(2) // original + resumed
	manifest := env.LatestManifest()
	if manifest.Resume == "" {
		t.Error("unfrozen task should launch with --resume")
	}
}

func TestComplete(t *testing.T) {
	env := NewTestEnv(t)

	env.CreateTask("complete-test")

	// 'c' for complete is in the detail modal.
	env.SendKeys("Tab")
	time.Sleep(300 * time.Millisecond)
	env.SendKeys("c")
	time.Sleep(500 * time.Millisecond)

	// Confirm completion with 'y'.
	env.SendKeys("y")

	// Complete involves SIGINT + 15s timeout, then kill-window.
	// DB state is updated after the window kill succeeds.
	env.WaitFor("task completed", 25*time.Second, func() bool {
		var state string
		var wid sql.NullString
		err := env.db.QueryRow("SELECT state, tmux_window FROM tasks WHERE name = ?", "complete-test").Scan(&state, &wid)
		return err == nil && state == "completed" && (!wid.Valid || wid.String == "")
	})
	env.WaitForTaskAttention("complete-test", "done")
}

func TestReconcileVanishedWindow(t *testing.T) {
	env := NewTestEnv(t)

	env.CreateTask("reconcile-test")

	// Kill the task window by session:name target.
	killTarget := env.krangSession + ":reconcile-test"
	if out, err := exec.Command("tmux", "kill-window", "-t", killTarget).CombinedOutput(); err != nil {
		t.Fatalf("killing window: %v: %s", err, out)
	}

	// Verify the window is actually gone from tmux.
	env.WaitFor("window killed", 3*time.Second, func() bool {
		return !env.TmuxWindowExists(env.krangSession, "reconcile-test")
	})

	// Reconcile tick fires every 10s. Task has a session ID so it
	// should become dormant (not failed).
	env.WaitFor("task reconciled to dormant", 25*time.Second, func() bool {
		var state string
		err := env.db.QueryRow("SELECT state FROM tasks WHERE name = ?", "reconcile-test").Scan(&state)
		return err == nil && state == "dormant"
	})
}

func TestCwdModeSelectsSubdirectory(t *testing.T) {
	env := NewTestEnv(t)

	// Create subdirectories so the CWD picker appears (instead of being skipped).
	repoA := filepath.Join(env.projectDir, "repo-a")
	repoB := filepath.Join(env.projectDir, "repo-b")
	for _, d := range []string{repoA, repoB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("creating subdir %s: %v", d, err)
		}
	}

	// Open wizard and type task name.
	env.SendKeys("n")
	time.Sleep(400 * time.Millisecond)
	env.SendKeys("cwd-test")
	time.Sleep(200 * time.Millisecond)

	// Enter to advance from Name tab to CWD tab.
	env.SendKeys("Enter")
	time.Sleep(400 * time.Millisecond)

	// CWD picker should show "." (selected by default), "repo-a", "repo-b".
	// Navigate down to "repo-a".
	env.SendKeys("j")
	time.Sleep(200 * time.Millisecond)

	// Submit from the CWD tab.
	env.SendKeys("Enter")

	env.WaitForTaskExists("cwd-test")
	env.WaitForTaskState("cwd-test", "active")

	// Verify the task's cwd in the DB is the subdirectory, not the root.
	taskCwd := env.TaskCwd("cwd-test")
	if taskCwd != repoA {
		t.Errorf("task cwd = %q, want %q", taskCwd, repoA)
	}

	// Verify fakeclaude was actually launched in the subdirectory.
	env.WaitForManifestCount(1)
	manifest := env.LatestManifest()
	if manifest == nil {
		t.Fatal("no fakeclaude manifest found")
	}
	if manifest.Cwd != repoA {
		t.Errorf("fakeclaude cwd = %q, want %q", manifest.Cwd, repoA)
	}
}

func TestForkNonWorkspace(t *testing.T) {
	env := NewTestEnv(t)

	env.CreateTask("fork-src")

	// Establish the source session by sending SessionStart.
	srcSessionID := env.TaskSessionID("fork-src")
	env.SendHook(map[string]interface{}{
		"session_id":      srcSessionID,
		"hook_event_name": "SessionStart",
		"cwd":             env.projectDir,
	})
	time.Sleep(300 * time.Millisecond)

	// Open detail modal and fork.
	env.SendKeys("Tab")
	time.Sleep(300 * time.Millisecond)
	env.SendKeys("d")
	time.Sleep(500 * time.Millisecond)

	// The fork form should be open. Accept the default fork name and submit.
	env.SendKeys("Enter")

	// Wait for the fork task to be created.
	env.WaitForTaskExists("fork-src-fork")
	env.WaitForTaskState("fork-src-fork", "active")

	// Verify fork lineage.
	if sourceID := env.TaskSourceID("fork-src-fork"); sourceID == "" {
		t.Error("fork task should have source_task_id set")
	}

	// Verify fakeclaude launched with --resume --fork-session.
	env.WaitForManifestCount(2) // original + fork
	manifest := env.LatestManifest()
	if manifest.Resume == "" {
		t.Error("fork should launch with --resume")
	}
	if !manifest.ForkSession {
		t.Error("fork should launch with --fork-session")
	}

	// Send SessionStart from the fork to trigger adoption.
	env.SendHook(map[string]interface{}{
		"session_id":      "new-fork-session-id",
		"hook_event_name": "SessionStart",
		"cwd":             env.projectDir,
	})

	// The fork task should adopt the new session ID.
	env.WaitFor("fork session adopted", 10*time.Second, func() bool {
		sid := env.TaskSessionID("fork-src-fork")
		return sid == "new-fork-session-id"
	})

	// Original task should be unaffected.
	if sid := env.TaskSessionID("fork-src"); sid != srcSessionID {
		t.Errorf("source task session_id changed from %q to %q", srcSessionID, sid)
	}
}

// ---------------------------------------------------------------------------
// Git workspace tests
// ---------------------------------------------------------------------------

func TestGitSingleRepoWorkspace(t *testing.T) {
	env := NewWorkspaceTestEnv(t, "single_repo", "git", []string{"alpha", "beta"})
	repoDir := filepath.Join(env.ReposDir(), "alpha")

	env.CreateSingleRepoTask("git-single")

	expectedWsDir := filepath.Join(env.WorkspacesDir(), "git-single")

	// DB state.
	if wsDir := env.TaskWorkspaceDir("git-single"); wsDir != expectedWsDir {
		t.Errorf("workspace_dir = %q, want %q", wsDir, expectedWsDir)
	}
	if cwd := env.TaskCwd("git-single"); cwd != expectedWsDir {
		t.Errorf("task cwd = %q, want %q", cwd, expectedWsDir)
	}

	// Fakeclaude process CWD.
	env.WaitForManifestCount(1)
	manifest := env.LatestManifest()
	if manifest == nil {
		t.Fatal("no fakeclaude manifest found")
	}
	if manifest.Cwd != expectedWsDir {
		t.Errorf("fakeclaude cwd = %q, want %q", manifest.Cwd, expectedWsDir)
	}

	// Workspace is a git worktree (has .git file, not directory).
	gitPath := filepath.Join(expectedWsDir, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		t.Fatalf("workspace missing .git: %v", err)
	}
	if info.IsDir() {
		t.Error(".git should be a file (worktree pointer), not a directory")
	}

	// Source repo should list the worktree.
	wtList := gitWorktreeList(t, repoDir)
	if !strings.Contains(wtList, expectedWsDir) {
		t.Errorf("worktree not listed in source repo:\n%s", wtList)
	}

	// Branch krang/git-single should exist in the source repo.
	if !gitBranchExists(repoDir, "krang/git-single") {
		t.Error("branch krang/git-single should exist in source repo")
	}

	// There should NOT be a nested alpha/ subdirectory (single_repo mode).
	if _, err := os.Stat(filepath.Join(expectedWsDir, "alpha")); err == nil {
		t.Error("single_repo should not create nested repo subdirectory")
	}
}

func TestGitMultiRepoWorkspace(t *testing.T) {
	env := NewWorkspaceTestEnv(t, "multi_repo", "git", []string{"alpha", "beta"})

	env.CreateMultiRepoTask("git-multi", 2)

	expectedWsDir := filepath.Join(env.WorkspacesDir(), "git-multi")

	// DB state.
	if wsDir := env.TaskWorkspaceDir("git-multi"); wsDir != expectedWsDir {
		t.Errorf("workspace_dir = %q, want %q", wsDir, expectedWsDir)
	}
	if cwd := env.TaskCwd("git-multi"); cwd != expectedWsDir {
		t.Errorf("task cwd = %q, want %q", cwd, expectedWsDir)
	}

	// Fakeclaude process CWD.
	env.WaitForManifestCount(1)
	manifest := env.LatestManifest()
	if manifest == nil {
		t.Fatal("no fakeclaude manifest found")
	}
	if manifest.Cwd != expectedWsDir {
		t.Errorf("fakeclaude cwd = %q, want %q", manifest.Cwd, expectedWsDir)
	}

	// Each repo should be a worktree under the workspace dir with a branch.
	for _, repo := range []string{"alpha", "beta"} {
		repoWtDir := filepath.Join(expectedWsDir, repo)
		repoSrc := filepath.Join(env.ReposDir(), repo)

		// Worktree .git file should exist.
		info, err := os.Stat(filepath.Join(repoWtDir, ".git"))
		if err != nil {
			t.Errorf("%s: missing .git: %v", repo, err)
			continue
		}
		if info.IsDir() {
			t.Errorf("%s: .git should be a file (worktree), not a directory", repo)
		}

		// Source repo should list the worktree.
		wtList := gitWorktreeList(t, repoSrc)
		if !strings.Contains(wtList, repoWtDir) {
			t.Errorf("%s: worktree not listed:\n%s", repo, wtList)
		}

		// Branch should exist.
		if !gitBranchExists(repoSrc, "krang/git-multi") {
			t.Errorf("%s: branch krang/git-multi should exist", repo)
		}
	}
}

func TestGitWorkspaceForkAndComplete(t *testing.T) {
	env := NewWorkspaceTestEnv(t, "single_repo", "git", []string{"alpha"})
	repoDir := filepath.Join(env.ReposDir(), "alpha")

	env.CreateSingleRepoTask("git-fork-src")

	srcWsDir := filepath.Join(env.WorkspacesDir(), "git-fork-src")
	env.WaitFor("workspace dir exists", 10*time.Second, func() bool {
		_, err := os.Stat(srcWsDir)
		return err == nil
	})

	// Make a commit in the source workspace so the fork branch has unpushed work.
	if err := os.WriteFile(filepath.Join(srcWsDir, "feature.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "feature.txt"},
		{"commit", "-m", "add feature"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = srcWsDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	// Establish the source session.
	srcSessionID := env.TaskSessionID("git-fork-src")
	env.SendHook(map[string]interface{}{
		"session_id":      srcSessionID,
		"hook_event_name": "SessionStart",
		"cwd":             srcWsDir,
	})
	time.Sleep(300 * time.Millisecond)

	// Fork via detail modal.
	env.SendKeys("Tab")
	time.Sleep(300 * time.Millisecond)
	env.SendKeys("d")
	time.Sleep(500 * time.Millisecond)
	env.SendKeys("Enter")

	env.WaitForTaskExists("git-fork-src-fork")
	env.WaitForTaskState("git-fork-src-fork", "active")

	forkWsDir := filepath.Join(env.WorkspacesDir(), "git-fork-src-fork")

	// Fork should have its own workspace dir and CWD.
	if wsDir := env.TaskWorkspaceDir("git-fork-src-fork"); wsDir != forkWsDir {
		t.Errorf("fork workspace_dir = %q, want %q", wsDir, forkWsDir)
	}
	if cwd := env.TaskCwd("git-fork-src-fork"); cwd != forkWsDir {
		t.Errorf("fork cwd = %q, want %q", cwd, forkWsDir)
	}

	// Fork workspace should be a git worktree.
	info, err := os.Stat(filepath.Join(forkWsDir, ".git"))
	if err != nil {
		t.Fatalf("fork workspace missing .git: %v", err)
	}
	if info.IsDir() {
		t.Error("fork .git should be a file (worktree), not a directory")
	}

	// Fork branch should exist.
	if !gitBranchExists(repoDir, "krang/git-fork-src-fork") {
		t.Error("branch krang/git-fork-src-fork should exist in source repo")
	}

	// Fakeclaude launched in fork workspace.
	env.WaitForManifestCount(2)
	manifest := env.LatestManifest()
	if manifest.Cwd != forkWsDir {
		t.Errorf("fork fakeclaude cwd = %q, want %q", manifest.Cwd, forkWsDir)
	}

	// --- Complete the fork task and verify cleanup ---

	// Dismiss any modal, select the fork task (second row), open detail, complete.
	env.SendKeys("Escape")
	time.Sleep(200 * time.Millisecond)
	env.SendKeys("j")
	time.Sleep(200 * time.Millisecond)
	env.SendKeys("Tab")
	time.Sleep(300 * time.Millisecond)
	env.SendKeys("c")
	time.Sleep(500 * time.Millisecond)
	env.SendKeys("y")

	env.WaitFor("fork completed", 25*time.Second, func() bool {
		var state string
		err := env.db.QueryRow("SELECT state FROM tasks WHERE name = ?", "git-fork-src-fork").Scan(&state)
		return err == nil && state == "completed"
	})

	// Fork workspace directory should be removed.
	env.WaitFor("fork workspace removed", 10*time.Second, func() bool {
		_, err := os.Stat(forkWsDir)
		return os.IsNotExist(err)
	})

	// Fork branch should be KEPT because it has unpushed commits (no remote).
	// git branch -d refuses to delete unmerged branches.
	if !gitBranchExists(repoDir, "krang/git-fork-src-fork") {
		t.Error("branch krang/git-fork-src-fork should be kept (unpushed commits)")
	}

	// Source worktree should NOT have been removed.
	wtList := gitWorktreeList(t, repoDir)
	if !strings.Contains(wtList, srcWsDir) {
		t.Error("source worktree should still be listed")
	}

	// Source workspace should still exist on disk.
	if _, err := os.Stat(srcWsDir); err != nil {
		t.Errorf("source workspace should still exist: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Jujutsu workspace tests
// ---------------------------------------------------------------------------

func TestJJSingleRepoWorkspace(t *testing.T) {
	env := NewWorkspaceTestEnv(t, "single_repo", "jj", []string{"alpha", "beta"})
	repoDir := filepath.Join(env.ReposDir(), "alpha")

	env.CreateSingleRepoTask("jj-single")

	expectedWsDir := filepath.Join(env.WorkspacesDir(), "jj-single")

	// DB state.
	if wsDir := env.TaskWorkspaceDir("jj-single"); wsDir != expectedWsDir {
		t.Errorf("workspace_dir = %q, want %q", wsDir, expectedWsDir)
	}
	if cwd := env.TaskCwd("jj-single"); cwd != expectedWsDir {
		t.Errorf("task cwd = %q, want %q", cwd, expectedWsDir)
	}

	// Fakeclaude process CWD.
	env.WaitForManifestCount(1)
	manifest := env.LatestManifest()
	if manifest == nil {
		t.Fatal("no fakeclaude manifest found")
	}
	if manifest.Cwd != expectedWsDir {
		t.Errorf("fakeclaude cwd = %q, want %q", manifest.Cwd, expectedWsDir)
	}

	// Workspace should have .jj directory (jj workspace).
	jjDir := filepath.Join(expectedWsDir, ".jj")
	if info, err := os.Stat(jjDir); err != nil || !info.IsDir() {
		t.Fatalf("workspace missing .jj directory: %v", err)
	}

	// Source repo should list this workspace.
	wsList := jjWorkspaceList(t, repoDir)
	if !strings.Contains(wsList, "jj-single") {
		t.Errorf("workspace 'jj-single' not in jj workspace list:\n%s", wsList)
	}

	// There should NOT be a nested alpha/ subdirectory (single_repo mode).
	if _, err := os.Stat(filepath.Join(expectedWsDir, "alpha")); err == nil {
		t.Error("single_repo should not create nested repo subdirectory")
	}
}

func TestJJMultiRepoWorkspace(t *testing.T) {
	env := NewWorkspaceTestEnv(t, "multi_repo", "jj", []string{"alpha", "beta"})

	env.CreateMultiRepoTask("jj-multi", 2)

	expectedWsDir := filepath.Join(env.WorkspacesDir(), "jj-multi")

	// DB state.
	if wsDir := env.TaskWorkspaceDir("jj-multi"); wsDir != expectedWsDir {
		t.Errorf("workspace_dir = %q, want %q", wsDir, expectedWsDir)
	}
	if cwd := env.TaskCwd("jj-multi"); cwd != expectedWsDir {
		t.Errorf("task cwd = %q, want %q", cwd, expectedWsDir)
	}

	// Fakeclaude process CWD.
	env.WaitForManifestCount(1)
	manifest := env.LatestManifest()
	if manifest == nil {
		t.Fatal("no fakeclaude manifest found")
	}
	if manifest.Cwd != expectedWsDir {
		t.Errorf("fakeclaude cwd = %q, want %q", manifest.Cwd, expectedWsDir)
	}

	// Each repo should be a jj workspace under the workspace dir.
	for _, repo := range []string{"alpha", "beta"} {
		repoWsDir := filepath.Join(expectedWsDir, repo)
		repoSrc := filepath.Join(env.ReposDir(), repo)

		// .jj directory should exist.
		if info, err := os.Stat(filepath.Join(repoWsDir, ".jj")); err != nil || !info.IsDir() {
			t.Errorf("%s: missing .jj directory: %v", repo, err)
			continue
		}

		// Source repo should list this workspace.
		wsList := jjWorkspaceList(t, repoSrc)
		if !strings.Contains(wsList, "jj-multi") {
			t.Errorf("%s: workspace 'jj-multi' not in jj workspace list:\n%s", repo, wsList)
		}
	}
}

func TestJJWorkspaceForkAndComplete(t *testing.T) {
	env := NewWorkspaceTestEnv(t, "single_repo", "jj", []string{"alpha"})
	repoDir := filepath.Join(env.ReposDir(), "alpha")

	env.CreateSingleRepoTask("jj-fork-src")

	srcWsDir := filepath.Join(env.WorkspacesDir(), "jj-fork-src")
	env.WaitFor("workspace dir exists", 10*time.Second, func() bool {
		_, err := os.Stat(srcWsDir)
		return err == nil
	})

	// Establish the source session.
	srcSessionID := env.TaskSessionID("jj-fork-src")
	env.SendHook(map[string]interface{}{
		"session_id":      srcSessionID,
		"hook_event_name": "SessionStart",
		"cwd":             srcWsDir,
	})
	time.Sleep(300 * time.Millisecond)

	// Source workspace should be listed.
	wsList := jjWorkspaceList(t, repoDir)
	if !strings.Contains(wsList, "jj-fork-src") {
		t.Errorf("source workspace not in jj workspace list:\n%s", wsList)
	}

	// Fork via detail modal.
	env.SendKeys("Tab")
	time.Sleep(300 * time.Millisecond)
	env.SendKeys("d")
	time.Sleep(500 * time.Millisecond)
	env.SendKeys("Enter")

	env.WaitForTaskExists("jj-fork-src-fork")
	env.WaitForTaskState("jj-fork-src-fork", "active")

	forkWsDir := filepath.Join(env.WorkspacesDir(), "jj-fork-src-fork")

	// Fork should have its own workspace dir and CWD.
	if wsDir := env.TaskWorkspaceDir("jj-fork-src-fork"); wsDir != forkWsDir {
		t.Errorf("fork workspace_dir = %q, want %q", wsDir, forkWsDir)
	}
	if cwd := env.TaskCwd("jj-fork-src-fork"); cwd != forkWsDir {
		t.Errorf("fork cwd = %q, want %q", cwd, forkWsDir)
	}

	// Fork workspace should have .jj directory.
	if info, err := os.Stat(filepath.Join(forkWsDir, ".jj")); err != nil || !info.IsDir() {
		t.Fatalf("fork workspace missing .jj: %v", err)
	}

	// Both workspaces should be listed.
	wsList = jjWorkspaceList(t, repoDir)
	if !strings.Contains(wsList, "jj-fork-src-fork") {
		t.Errorf("fork workspace not in jj workspace list:\n%s", wsList)
	}

	// Fakeclaude launched in fork workspace.
	env.WaitForManifestCount(2)
	manifest := env.LatestManifest()
	if manifest.Cwd != forkWsDir {
		t.Errorf("fork fakeclaude cwd = %q, want %q", manifest.Cwd, forkWsDir)
	}

	// --- Complete the fork task and verify cleanup ---

	env.SendKeys("Escape")
	time.Sleep(200 * time.Millisecond)
	env.SendKeys("j")
	time.Sleep(200 * time.Millisecond)
	env.SendKeys("Tab")
	time.Sleep(300 * time.Millisecond)
	env.SendKeys("c")
	time.Sleep(500 * time.Millisecond)
	env.SendKeys("y")

	env.WaitFor("fork completed", 25*time.Second, func() bool {
		var state string
		err := env.db.QueryRow("SELECT state FROM tasks WHERE name = ?", "jj-fork-src-fork").Scan(&state)
		return err == nil && state == "completed"
	})

	// Fork workspace directory should be removed.
	env.WaitFor("fork workspace removed", 10*time.Second, func() bool {
		_, err := os.Stat(forkWsDir)
		return os.IsNotExist(err)
	})

	// Fork workspace should be forgotten by jj.
	wsList = jjWorkspaceList(t, repoDir)
	if strings.Contains(wsList, "jj-fork-src-fork") {
		t.Errorf("fork workspace should be forgotten, but still listed:\n%s", wsList)
	}

	// Source workspace should still be listed.
	if !strings.Contains(wsList, "jj-fork-src") {
		t.Errorf("source workspace should still be listed:\n%s", wsList)
	}

	// Source workspace should still exist on disk.
	if _, err := os.Stat(srcWsDir); err != nil {
		t.Errorf("source workspace should still exist: %v", err)
	}
}
