package tmux

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

const (
	WindowPrefix    = "K!"
	CompanionPrefix = "KF!"
)

type WindowInfo struct {
	ID   string // stable @N identifier
	Name string
}

func WindowName(taskName string) string {
	return WindowPrefix + taskName
}

func CreateWindow(session, name, cwd, shellCommand string) (string, error) {
	cmd := exec.Command(
		"tmux", "new-window",
		"-a",
		"-c", cwd,
		"-t", session+":",
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

func CompanionWindowName(taskName string) string {
	return CompanionPrefix + taskName
}

// FindCompanions returns window IDs for any KF!<taskName> windows in the given session.
func FindCompanions(session, taskName string) []string {
	windows, err := ListWindows(session)
	if err != nil {
		return nil
	}
	target := CompanionPrefix + taskName
	var companions []string
	for _, w := range windows {
		if w.Name == target {
			companions = append(companions, w.ID)
		}
	}
	return companions
}

// CreateWindowAfter creates a new window immediately after the given window.
func CreateWindowAfter(afterWindowID, name, cwd string) (string, error) {
	cmd := exec.Command(
		"tmux", "new-window",
		"-a",
		"-c", cwd,
		"-t", afterWindowID,
		"-n", name,
		"-P", "-F", "#{window_id}",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("creating window %s after %s: %s: %w", name, afterWindowID, strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// CompactWindows renumbers all windows in the session sequentially.
func CompactWindows(session string) error {
	cmd := exec.Command("tmux", "move-window", "-r", "-t", session+":")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compacting windows in %s: %s: %w", session, strings.TrimSpace(string(out)), err)
	}
	return nil
}

var fgColorPattern = regexp.MustCompile(`fg=[^],]*`)

func SetWindowStyle(windowID, fgColor string) error {
	// Set window-status-style for simple/default themes.
	style := "fg=" + fgColor
	cmd := exec.Command("tmux", "set-window-option", "-t", windowID, "window-status-style", style)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("setting window-status-style on %s: %s: %w", windowID, strings.TrimSpace(string(out)), err)
	}

	// For themes with inline #[fg=...] in the format string, override the
	// format with our color substituted in. Only set if the global format
	// contains fg= references; otherwise the style above is sufficient.
	globalFormat, err := globalOption("window-status-format")
	if err == nil && fgColorPattern.MatchString(globalFormat) {
		modified := fgColorPattern.ReplaceAllString(globalFormat, "fg="+fgColor)
		cmd = exec.Command("tmux", "set-window-option", "-t", windowID, "window-status-format", modified)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("setting window-status-format on %s: %s: %w", windowID, strings.TrimSpace(string(out)), err)
		}
	}

	return nil
}

func ClearWindowStyle(windowID string) error {
	for _, option := range []string{"window-status-style", "window-status-format"} {
		cmd := exec.Command("tmux", "set-window-option", "-u", "-t", windowID, option)
		// Ignore errors — the option may not have been set.
		_ = cmd.Run()
	}
	return nil
}

func globalOption(name string) (string, error) {
	cmd := exec.Command("tmux", "show-options", "-gv", name)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func RenameWindow(windowID, newName string) error {
	cmd := exec.Command("tmux", "rename-window", "-t", windowID, newName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("renaming window %s: %s: %w", windowID, strings.TrimSpace(string(out)), err)
	}
	return nil
}
