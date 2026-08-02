//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// directTmuxCall matches a tmux invocation that does not go through
// tmuxOn/TestEnv.tmux, and so would target the default tmux server.
var directTmuxCall = regexp.MustCompile(`exec\.Command\(\s*"tmux"`)

// TestNoDirectTmuxInvocations fails if any test in this package drives
// tmux without pinning a socket.
//
// The integration suite must never touch the developer's default tmux
// server: it creates and kills sessions, and it sets HOME and
// KRANG_CLAUDE_CMD in the server's global environment. On a shared
// server those globals are inherited by every window opened afterwards,
// including ones belonging to a real krang instance, which then launches
// the fake Claude binary against a temp-dir HOME.
func TestNoDirectTmuxInvocations(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing package sources: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no package sources found; the guard would pass vacuously")
	}

	for _, file := range files {
		// This file quotes the pattern it searches for.
		if file == "isolation_test.go" {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		// harness_test.go defines tmuxOn, the one sanctioned call site.
		body := string(source)
		if file == "harness_test.go" {
			body = strings.Replace(body, sanctionedTmuxCall, "", 1)
		}
		if loc := directTmuxCall.FindStringIndex(body); loc != nil {
			line := 1 + strings.Count(body[:loc[0]], "\n")
			t.Errorf("%s:%d invokes tmux directly; use env.tmux(...) or "+
				"tmuxOn(socket, ...) so the call targets the test's "+
				"private tmux server", file, line)
		}
	}
}

// sanctionedTmuxCall is the single exec.Command("tmux", ...) the suite is
// allowed to contain — the body of tmuxOn, which always supplies -L.
const sanctionedTmuxCall = `return exec.Command("tmux", append([]string{"-L", socket}, args...)...)`
