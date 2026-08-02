package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dpetersen/krang/internal/pathutil"
	"github.com/dpetersen/krang/internal/tmux"
	"github.com/dpetersen/krang/internal/workspaceclient"
)

// The launch guards exist because krang's startup used to mutate before
// it validated: it renamed the caller's tmux session in its ninth
// statement and only discovered whether it could actually draw a TUI in
// its last one. Running bare `krang` inside a task window therefore
// renamed a live instance's session out from under it.
//
// These tests pin both halves — that the refusals happen, and that they
// happen before anything is touched.

// countingProbe records whether a guard probe was consulted, so a test
// can assert that a refusal short-circuited before reaching it.
type countingProbe struct {
	calls  int
	result bool
}

func (p *countingProbe) fn() func() bool {
	return func() bool {
		p.calls++
		return p.result
	}
}

// AC: bare krang with KRANG_STATEFILE set exits non-zero
// unconditionally. Unconditionally means the statefile is checked first,
// ahead of the probes that shell out — so a refused launch runs zero
// tmux commands by construction, not by luck.
func TestStatefileRefusalHappensBeforeAnyProbe(t *testing.T) {
	terminal := &countingProbe{result: true}
	tmuxProbe := &countingProbe{result: true}

	guards := launchGuards{
		Statefile:         "/tmp/krang-state.json",
		TerminalAvailable: terminal.fn(),
		InsideTmux:        tmuxProbe.fn(),
	}

	err := guards.check()
	if err == nil {
		t.Fatal("launching inside a task window was allowed")
	}
	if tmuxProbe.calls != 0 {
		t.Errorf("consulted tmux %d times while refusing a nested launch; want 0", tmuxProbe.calls)
	}
	if terminal.calls != 0 {
		t.Errorf("consulted the terminal %d times while refusing a nested launch; want 0", terminal.calls)
	}
}

// The refusal has to be actionable by a caller who did this on purpose,
// and has to say that no flag will change its mind — otherwise the next
// thing that happens is a search for one.
func TestNestedLaunchRefusalNamesTheEscapeHatch(t *testing.T) {
	guards := launchGuards{Statefile: "/tmp/krang-state.json"}

	err := guards.check()
	if err == nil {
		t.Fatal("launching inside a task window was allowed")
	}

	for _, want := range []string{
		"KRANG_STATEFILE",
		"env -u KRANG_STATEFILE krang",
		"no override",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q:\n%s", want, err)
		}
	}
}

// A nested launch is refused with a statefile pointing at nothing, too.
// The variable's presence is what identifies a task window; whether the
// instance that set it is still alive changes nothing about the damage.
func TestNestedLaunchRefusedEvenWithAStaleStatefile(t *testing.T) {
	guards := launchGuards{
		Statefile:         filepath.Join(t.TempDir(), "does-not-exist.json"),
		TerminalAvailable: func() bool { return true },
		InsideTmux:        func() bool { return true },
	}

	if err := guards.check(); err == nil {
		t.Fatal("a stale statefile was treated as permission to launch")
	}
}

// AC: a non-TTY launch exits before any tmux rename or state creation.
// At the guard level that means the terminal check precedes the tmux
// probe, which is the first thing in startup that runs a tmux command.
func TestNonTerminalLaunchIsRefusedBeforeTouchingTmux(t *testing.T) {
	tmuxProbe := &countingProbe{result: true}

	guards := launchGuards{
		TerminalAvailable: func() bool { return false },
		InsideTmux:        tmuxProbe.fn(),
	}

	err := guards.check()
	if err == nil {
		t.Fatal("launching without a terminal was allowed")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("refusal does not explain the missing terminal: %s", err)
	}
	if tmuxProbe.calls != 0 {
		t.Errorf("consulted tmux %d times while refusing a non-TTY launch; want 0", tmuxProbe.calls)
	}
}

func TestLaunchOutsideTmuxIsRefused(t *testing.T) {
	guards := launchGuards{
		TerminalAvailable: func() bool { return true },
		InsideTmux:        func() bool { return false },
	}

	err := guards.check()
	if err == nil {
		t.Fatal("launching outside tmux was allowed")
	}
	if !strings.Contains(err.Error(), "tmux") {
		t.Errorf("refusal does not mention tmux: %s", err)
	}
}

func TestGuardsPassForAnOrdinaryLaunch(t *testing.T) {
	guards := launchGuards{
		TerminalAvailable: func() bool { return true },
		InsideTmux:        func() bool { return true },
	}

	if err := guards.check(); err != nil {
		t.Fatalf("an ordinary launch was refused: %s", err)
	}
}

// isolateLaunch points every path and socket runTUI could write at
// throwaway directories, and returns a function that fails the test if
// any of them were created. HOME covers the data and state directories
// (pathutil derives both from it), TMUX_TMPDIR covers the tmux server
// socket, and KRANG_TMUX_SOCKET keeps any tmux call that does escape off
// the developer's default server.
func isolateLaunch(t *testing.T) func() {
	t.Helper()

	home := t.TempDir()
	tmuxTmp := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %s", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("TMUX_TMPDIR", tmuxTmp)
	t.Setenv(tmux.SocketEnvVar, "krang-guard-test")
	t.Setenv("KRANG_CONFIG", filepath.Join(home, "config.yaml"))
	t.Setenv("KRANG_DB", filepath.Join(home, "krang.db"))

	return func() {
		t.Helper()

		for _, path := range []string{
			pathutil.DataDir(cwd),
			pathutil.StateDir(cwd),
			filepath.Join(home, ".local"),
			filepath.Join(home, ".config"),
			filepath.Join(home, "krang.db"),
			filepath.Join(home, "config.yaml"),
		} {
			if _, err := os.Stat(path); err == nil {
				t.Errorf("a refused launch created %s", path)
			}
		}

		// A tmux server only comes into existence when something creates
		// a session on it, so an empty socket directory is proof that no
		// tmux mutation ran. Reads (has-session, display-message) would
		// have failed against the missing server rather than starting one.
		entries, err := os.ReadDir(tmuxTmp)
		if err != nil {
			t.Fatalf("reading tmux socket dir: %s", err)
		}
		if len(entries) != 0 {
			t.Errorf("a refused launch left %d entries in the tmux socket dir; want 0", len(entries))
		}
	}
}

// AC: bare krang with KRANG_STATEFILE set exits non-zero, and the test
// asserts zero tmux mutations and zero state-dir writes. This drives
// runTUI itself rather than the guard struct, so it also pins that the
// guard is actually wired in ahead of the startup body.
func TestRunTUIRefusesInsideATaskWindowWithoutTouchingAnything(t *testing.T) {
	verify := isolateLaunch(t)
	t.Setenv(workspaceclient.StateFileEnv, filepath.Join(t.TempDir(), "krang-state.json"))

	// Both would otherwise pass, so the statefile is the only thing that
	// can be refusing here.
	restore := stubProbes(t, true, true)
	defer restore()

	err := runTUI(nil, nil)
	if err == nil {
		t.Fatal("runTUI proceeded inside a task window")
	}
	if !strings.Contains(err.Error(), "env -u KRANG_STATEFILE krang") {
		t.Errorf("refusal does not suggest the escape hatch: %s", err)
	}
	assertExitsNonZero(t, err)

	verify()
}

// AC: a non-TTY launch exits before any tmux rename or state creation.
func TestRunTUIRefusesWithoutATerminalWithoutTouchingAnything(t *testing.T) {
	verify := isolateLaunch(t)
	t.Setenv(workspaceclient.StateFileEnv, "")

	restore := stubProbes(t, false, true)
	defer restore()

	err := runTUI(nil, nil)
	if err == nil {
		t.Fatal("runTUI proceeded without a terminal")
	}
	assertExitsNonZero(t, err)

	verify()
}

func stubProbes(t *testing.T, terminal, tmuxUp bool) func() {
	t.Helper()

	savedTerminal, savedTmux := terminalAvailable, insideTmux
	terminalAvailable = func() bool { return terminal }
	insideTmux = func() bool { return tmuxUp }
	return func() {
		terminalAvailable, insideTmux = savedTerminal, savedTmux
	}
}

// Execute maps a plain error to os.Exit(1) and defers to ExitCode() when
// the error carries one. A guard refusal must not carry a zero code,
// which is the only way a returned error could still exit successfully.
func assertExitsNonZero(t *testing.T, err error) {
	t.Helper()

	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) && coded.ExitCode() == 0 {
		t.Error("refusal carries exit code 0")
	}
}
