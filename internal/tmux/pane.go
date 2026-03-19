package tmux

import (
	"fmt"
	"os/exec"
	"strings"
)

func CapturePane(windowID string, lines int) (string, error) {
	startLine := fmt.Sprintf("-%d", lines)
	cmd := exec.Command(
		"tmux", "capture-pane",
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
	cmd := exec.Command("tmux", "send-keys", "-t", windowID, keys, "Enter")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sending keys to %s: %s: %w", windowID, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func WindowExists(windowID string) bool {
	cmd := exec.Command(
		"tmux", "display-message",
		"-t", windowID,
		"-p", "#{window_id}",
	)
	return cmd.Run() == nil
}

func PanePID(windowID string) (int, error) {
	cmd := exec.Command(
		"tmux", "display-message",
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

func PaneDead(windowID string) bool {
	cmd := exec.Command(
		"tmux", "display-message",
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
	cmd := exec.Command(
		"tmux", "display-message",
		"-t", session,
		"-p", "#{window_id}",
	)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("getting active window for %s: %w", session, err)
	}
	return strings.TrimSpace(string(out)), nil
}
