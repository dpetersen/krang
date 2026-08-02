package tmux

import (
	"fmt"
	"os"
	"strings"
)

func CapturePane(windowID string, lines int) (string, error) {
	startLine := fmt.Sprintf("-%d", lines)
	cmd := command(
		"capture-pane",
		"-p",
		"-t", windowID,
		"-S", startLine,
	)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("capturing pane %s: %w", windowID, err)
	}
	return string(out), nil
}

func SendKeys(windowID, keys string) error {
	cmd := command("send-keys", "-t", windowID, keys, "Enter")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sending keys to %s: %s: %w", windowID, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func SendEnter(windowID string) error {
	cmd := command("send-keys", "-t", windowID, "Enter")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sending enter to %s: %s: %w", windowID, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func WindowExists(windowID string) bool {
	cmd := command(
		"display-message",
		"-t", windowID,
		"-p", "#{window_id}",
	)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// tmux 3.6+ returns exit 0 with empty output for non-existent
	// targets instead of a non-zero exit code. Check that the
	// output actually contains a window ID.
	return strings.TrimSpace(string(out)) != ""
}

func PanePID(windowID string) (int, error) {
	cmd := command(
		"display-message",
		"-t", windowID,
		"-p", "#{pane_pid}",
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("getting pane PID for %s: %w", windowID, err)
	}
	pid := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pid); err != nil {
		return 0, fmt.Errorf("parsing pane PID: %w", err)
	}
	return pid, nil
}

// CurrentWindowID returns the window ID of the pane this process is
// running in, using the TMUX_PANE environment variable.
func CurrentWindowID() (string, error) {
	paneID := os.Getenv("TMUX_PANE")
	if paneID == "" {
		return "", fmt.Errorf("TMUX_PANE not set")
	}
	cmd := command(
		"display-message",
		"-t", paneID,
		"-p", "#{window_id}",
	)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("getting window ID for pane %s: %w", paneID, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func PaneDead(windowID string) bool {
	cmd := command(
		"display-message",
		"-t", windowID,
		"-p", "#{pane_dead}",
	)
	out, err := cmd.Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) == "1"
}

func ActiveWindowID(session string) (string, error) {
	cmd := command(
		"display-message",
		"-t", session,
		"-p", "#{window_id}",
	)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("getting active window for %s: %w", session, err)
	}
	return strings.TrimSpace(string(out)), nil
}
