package tmux

import (
	"fmt"
	"os/exec"
	"strings"
)

const WindowPrefix = "K!"

type WindowInfo struct {
	ID   string // stable @N identifier
	Name string
}

func WindowName(taskName string) string {
	return WindowPrefix + taskName
}

func CreateWindow(session, name, shellCommand string) (string, error) {
	cmd := exec.Command(
		"tmux", "new-window",
		"-t", session,
		"-n", name,
		"-P", "-F", "#{window_id}",
		shellCommand,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("creating window %s: %s: %w", name, strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func MoveWindow(windowID, targetSession string) error {
	cmd := exec.Command("tmux", "move-window", "-s", windowID, "-t", targetSession+":")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("moving window %s to %s: %s: %w", windowID, targetSession, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func KillWindow(windowID string) error {
	cmd := exec.Command("tmux", "kill-window", "-t", windowID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("killing window %s: %s: %w", windowID, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func ListWindows(session string) ([]WindowInfo, error) {
	cmd := exec.Command(
		"tmux", "list-windows",
		"-t", session,
		"-F", "#{window_id}\t#{window_name}",
	)
	out, err := cmd.Output()
	if err != nil {
		// Session might not exist or have no windows.
		return nil, nil
	}

	var windows []WindowInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		windows = append(windows, WindowInfo{ID: parts[0], Name: parts[1]})
	}
	return windows, nil
}

func RenameWindow(windowID, newName string) error {
	cmd := exec.Command("tmux", "rename-window", "-t", windowID, newName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("renaming window %s: %s: %w", windowID, strings.TrimSpace(string(out)), err)
	}
	return nil
}
