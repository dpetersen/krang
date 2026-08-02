package tmux

import (
	"os"
	"os/exec"
)

// SocketEnvVar names the environment variable that pins krang to a
// specific tmux server. When set, every tmux invocation targets that
// server via "-L <name>" instead of the user's default server.
//
// Integration tests set this so that a test run cannot create, kill, or
// reconfigure anything on the developer's real tmux server — including
// the krang instance they may be running the tests from.
const SocketEnvVar = "KRANG_TMUX_SOCKET"

// command builds a tmux invocation targeting the configured server.
//
// Every tmux call in this package must go through this helper. A bare
// exec.Command("tmux", ...) silently escapes the socket pin and lands on
// the default server; TestNoDirectTmuxInvocations enforces that.
func command(args ...string) *exec.Cmd {
	if socket := os.Getenv(SocketEnvVar); socket != "" {
		return exec.Command("tmux", append([]string{"-L", socket}, args...)...)
	}
	return exec.Command("tmux", args...)
}
