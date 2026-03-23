package task

import (
	"testing"

	"github.com/dpetersen/krang/internal/db"
)

const testStateFile = "/tmp/krang-state.json"
const statePrefix = "export KRANG_STATEFILE='/tmp/krang-state.json'; "

func TestBuildClaudeCommandDefaults(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, false, "safehouse", testStateFile)
	expected := statePrefix + "safehouse claude --session-id sess-123 --name 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandResume(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, true, "safehouse", testStateFile)
	expected := statePrefix + "safehouse claude --resume 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandNoSandbox(t *testing.T) {
	flags := db.TaskFlags{NoSandbox: true}
	cmd := buildClaudeCommand("sess-123", "my-task", flags, false, "safehouse", testStateFile)
	expected := statePrefix + "claude --session-id sess-123 --name 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandSkipPermissions(t *testing.T) {
	flags := db.TaskFlags{DangerouslySkipPermissions: true}
	cmd := buildClaudeCommand("sess-123", "my-task", flags, false, "safehouse", testStateFile)
	expected := statePrefix + "safehouse claude --session-id sess-123 --name 'my-task' --dangerously-skip-permissions; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandAllFlags(t *testing.T) {
	flags := db.TaskFlags{NoSandbox: true, DangerouslySkipPermissions: true}
	cmd := buildClaudeCommand("sess-123", "my-task", flags, true, "safehouse", testStateFile)
	expected := statePrefix + "claude --resume 'my-task' --dangerously-skip-permissions; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandResumeNoName(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, true, "safehouse", testStateFile)
	if expected := "safehouse claude --resume 'my-task'"; !contains(cmd, expected) {
		t.Errorf("resume command should use name, not session ID:\n  %s", cmd)
	}
	if contains(cmd, "sess-123") {
		t.Errorf("resume command should not contain session ID:\n  %s", cmd)
	}
}

func TestBuildClaudeCommandCustomSandbox(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, false, "safehouse --append-profile ~/.config/safehouse/allow-nah.sb", testStateFile)
	expected := statePrefix + "safehouse --append-profile ~/.config/safehouse/allow-nah.sb claude --session-id sess-123 --name 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandEmptySandbox(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, false, "", testStateFile)
	expected := statePrefix + "claude --session-id sess-123 --name 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandNoStateFile(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, false, "safehouse", "")
	expected := "safehouse claude --session-id sess-123 --name 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
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
