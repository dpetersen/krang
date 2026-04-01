package task

import (
	"testing"

	"github.com/dpetersen/krang/internal/config"
	"github.com/dpetersen/krang/internal/db"
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
	expected := statePrefix + "safehouse claude --resume 'my-task'; echo ''; echo 'Claude exited. Press Enter to close.'; read"
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
	expected := statePrefix + "claude --resume 'my-task' --dangerously-skip-permissions; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandResumeNoName(t *testing.T) {
	cmd := buildClaudeCommand("sess-123", "my-task", db.TaskFlags{}, true, "safehouse", testStateFile, sandboxTemplateData{}, "")
	if expected := "safehouse claude --resume 'my-task'"; !contains(cmd, expected) {
		t.Errorf("resume command should use name, not session ID:\n  %s", cmd)
	}
	if contains(cmd, "sess-123") {
		t.Errorf("resume command should not contain session ID:\n  %s", cmd)
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
	expected := statePrefix + "safehouse claude --resume 'source-sess-id' --fork-session; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandForkNoSandbox(t *testing.T) {
	cmd := buildClaudeCommand("", "fork-task", db.TaskFlags{}, false, "", testStateFile, sandboxTemplateData{}, "source-sess-id")
	expected := statePrefix + "claude --resume 'source-sess-id' --fork-session; echo ''; echo 'Claude exited. Press Enter to close.'; read"
	if cmd != expected {
		t.Errorf("expected:\n  %s\ngot:\n  %s", expected, cmd)
	}
}

func TestBuildClaudeCommandForkWithFlags(t *testing.T) {
	flags := db.TaskFlags{DangerouslySkipPermissions: true, Debug: true}
	cmd := buildClaudeCommand("", "fork-task", flags, false, "safehouse", testStateFile, sandboxTemplateData{}, "source-sess-id")
	if !contains(cmd, "--resume 'source-sess-id' --fork-session") {
		t.Errorf("fork command missing --resume --fork-session:\n  %s", cmd)
	}
	if !contains(cmd, "--dangerously-skip-permissions") {
		t.Errorf("fork command missing --dangerously-skip-permissions:\n  %s", cmd)
	}
	if !contains(cmd, "KRANG_DEBUG=1") {
		t.Errorf("fork command missing KRANG_DEBUG:\n  %s", cmd)
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
