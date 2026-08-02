package tmux

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCommandTargetsDefaultServerWhenUnset(t *testing.T) {
	t.Setenv(SocketEnvVar, "")

	got := command("list-windows", "-t", "foo").Args
	want := []string{"tmux", "list-windows", "-t", "foo"}
	if !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCommandTargetsSocketWhenSet(t *testing.T) {
	t.Setenv(SocketEnvVar, "krang-test-1234")

	got := command("list-windows", "-t", "foo").Args
	want := []string{"tmux", "-L", "krang-test-1234", "list-windows", "-t", "foo"}
	if !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// TestNoDirectTmuxInvocations fails if anything in this package shells
// out without going through command(). A bare exec.Command bypasses
// KRANG_TMUX_SOCKET and lands on the user's default tmux server, which
// is how the integration suite used to clobber a live krang instance.
func TestNoDirectTmuxInvocations(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing package sources: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no package sources found; the guard would pass vacuously")
	}

	for _, file := range files {
		if file == "command.go" || file == "command_test.go" {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		if strings.Contains(string(source), "exec.Command(") {
			t.Errorf("%s calls exec.Command directly; use command() so the "+
				"invocation honors %s", file, SocketEnvVar)
		}
	}
}
