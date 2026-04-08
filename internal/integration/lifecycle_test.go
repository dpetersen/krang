//go:build integration

package integration

import (
	"database/sql"
	"os/exec"
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

	// Freeze: state is set to dormant BEFORE the window is killed.
	// The graceful shutdown takes up to 5s (SIGINT + timeout).
	// Wait for tmux_window to be cleared, which happens AFTER the kill.
	env.WaitFor("tmux_window cleared after freeze", 15*time.Second, func() bool {
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
