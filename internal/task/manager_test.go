package task

import (
	"testing"

	"github.com/dpetersen/krang/internal/db"
)

func TestBuildClaudeCommandDefaults(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, false)
	expected := "safehouse claude --session-id sess-123 --name 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandResume(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, true)
	expected := "safehouse claude --resume 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandNoSandbox(t *testing.T) {
	flags := db.TaskFlags{NoSandbox: true}
	cmd := buildClaudeCommand("sess-123", "my-task", flags, false)
	expected := "claude --session-id sess-123 --name 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandSkipPermissions(t *testing.T) {
	flags := db.TaskFlags{DangerouslySkipPermissions: true}
	cmd := buildClaudeCommand("sess-123", "my-task", flags, false)
	expected := "safehouse claude --session-id sess-123 --name 'my-task' --dangerously-skip-permissions; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandAllFlags(t *testing.T) {
	flags := db.TaskFlags{NoSandbox: true, DangerouslySkipPermissions: true}
	cmd := buildClaudeCommand("sess-123", "my-task", flags, true)
	expected := "claude --resume 'my-task' --dangerously-skip-permissions; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandResumeNoName(t *testing.T) {
	// Resume doesn't include --name or --session-id, just --resume with the name.
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, true)
	if expected := "safehouse claude --resume 'my-task'"; !contains(cmd, expected) {
		t.Errorf("resume command should use name, not session ID:\n  %s", cmd)
	}
	if contains(cmd, "sess-123") {
		t.Errorf("resume command should not contain session ID:\n  %s", cmd)
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
