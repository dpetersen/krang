package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/dpetersen/krang/internal/workspaceclient"
	"github.com/spf13/cobra"
)

// The behaviour of the workspace commands is tested in
// internal/workspaceclient, where it can be driven against a server
// without a process to exit. What is left to check here is the wiring —
// that the subcommands exist under the right parent with the right
// flags, that their help is usable on its own, and that an exit code
// makes it out through cobra instead of being flattened to 1.

func workspaceSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()
	for _, sub := range workspaceCmd.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	t.Fatalf("krang workspace has no %q subcommand", name)
	return nil
}

// AC: the group is a sibling of setup and teardown, so it runs outside
// the TUI rather than being something the TUI dispatches.
func TestWorkspaceIsATopLevelCommandGroup(t *testing.T) {
	var found bool
	for _, sub := range rootCmd.Commands() {
		if sub.Name() == "workspace" {
			found = true
		}
	}
	if !found {
		t.Fatal("krang has no top-level workspace command")
	}

	for _, name := range []string{"list", "add", "remove", "repos"} {
		workspaceSubcommand(t, name)
	}
}

func TestSubcommandsCarryTheFlagsTheirEndpointAccepts(t *testing.T) {
	common := []string{"task", "cwd", "json"}
	cases := map[string][]string{
		"list":   common,
		"repos":  common,
		"add":    append([]string{"repo", "label", "base"}, common...),
		"remove": append([]string{"dir", "repo", "label", "force"}, common...),
	}

	for name, wanted := range cases {
		cmd := workspaceSubcommand(t, name)
		for _, flag := range wanted {
			if cmd.Flags().Lookup(flag) == nil {
				t.Errorf("krang workspace %s has no --%s", name, flag)
			}
		}
	}

	// repos and list take nothing beyond how to name the task and how to
	// print: there is nothing else to ask them.
	for _, name := range []string{"list", "repos"} {
		cmd := workspaceSubcommand(t, name)
		for _, unwanted := range []string{"repo", "label", "dir", "base", "force"} {
			if cmd.Flags().Lookup(unwanted) != nil {
				t.Errorf("krang workspace %s has an unexpected --%s", name, unwanted)
			}
		}
	}

	// --force belongs to remove, not add: there is nothing about adding a
	// working copy that destroys anything.
	if workspaceSubcommand(t, "add").Flags().Lookup("force") != nil {
		t.Error("krang workspace add has a --force flag; force is removal's")
	}
}

// AC: one --help is enough to use the subcommand — it has to name the
// endpoint, the defaults, and what the exit codes mean.
func TestEverySubcommandHelpIsSelfContained(t *testing.T) {
	endpoints := map[string]string{
		"list":   "GET /api/workspace",
		"repos":  "GET /api/workspace/repos",
		"add":    "POST /api/workspace/add",
		"remove": "DELETE /api/workspace/slot",
	}

	for name, endpoint := range endpoints {
		help := workspaceSubcommand(t, name).Long
		for _, want := range []string{
			endpoint,
			"KRANG_STATEFILE",
			"--cwd defaults",
			"--json",
			workspaceclient.ExitCodeHelp,
		} {
			if !strings.Contains(help, want) {
				t.Errorf("krang workspace %s --help never mentions %q", name, want)
			}
		}
		if !strings.Contains(help, "DO NOT blindly retry") {
			t.Errorf("krang workspace %s --help does not warn about the unknown-applied code", name)
		}
	}
}

// A failure has to reach the shell as its own code. Without this, every
// refusal would look like every other error.
func TestWorkspaceFailurePropagatesItsExitCode(t *testing.T) {
	// No state file means no instance to talk to, which is exit 1 — the
	// cheapest failure to trigger, and the one an agent hits by running
	// the command in the wrong place.
	t.Setenv(workspaceclient.StateFileEnv, "")

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"workspace", "list"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	err := rootCmd.Execute()

	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) {
		t.Fatalf("error = %v, want one carrying an exit code", err)
	}
	if coded.ExitCode() != workspaceclient.ExitError {
		t.Errorf("exit code = %d, want %d", coded.ExitCode(), workspaceclient.ExitError)
	}
	if !strings.Contains(stderr.String(), workspaceclient.StateFileEnv) {
		t.Errorf("stderr = %q, want the actionable diagnosis", stderr.String())
	}
}
