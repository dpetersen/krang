package tmux

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

type WindowInfo struct {
	ID             string // stable @N identifier
	Index          string // display index (e.g. "0", "1", "2")
	Name           string
	KrangTask      string // value of @krang-task option, empty if not set
	KrangCompanion string // value of @krang-companion option, empty if not set
}

func WindowName(taskName string) string {
	return taskName
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
	if windowID == "" {
		return fmt.Errorf("refusing to move window: empty window ID (tmux would move the current window)")
	}
	cmd := exec.Command("tmux", "move-window", "-s", windowID, "-t", targetSession+":")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("moving window %s to %s: %s: %w", windowID, targetSession, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func KillWindow(windowID string) error {
	if windowID == "" {
		return fmt.Errorf("refusing to kill window: empty window ID (tmux would kill the current window)")
	}
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
		"-F", "#{window_id}\t#{window_index}\t#{window_name}\t#{@krang-task}\t#{@krang-companion}",
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
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 3 {
			continue
		}
		w := WindowInfo{ID: parts[0], Index: parts[1], Name: parts[2]}
		if len(parts) > 3 {
			w.KrangTask = parts[3]
		}
		if len(parts) > 4 {
			w.KrangCompanion = parts[4]
		}
		windows = append(windows, w)
	}
	return windows, nil
}

// WindowIndexes returns a map of window ID to display index for the session.
func WindowIndexes(session string) map[string]string {
	windows, err := ListWindows(session)
	if err != nil {
		return nil
	}
	m := make(map[string]string, len(windows))
	for _, w := range windows {
		m[w.ID] = w.Index
	}
	return m
}

func CompanionWindowName(taskName string) string {
	return taskName + "+"
}

// FindCompanions returns window IDs for companion windows associated with
// the given task, identified by the @krang-companion tmux user option.
func FindCompanions(session, taskName string) []string {
	windows, err := ListWindows(session)
	if err != nil {
		return nil
	}
	var companions []string
	for _, w := range windows {
		if w.KrangCompanion == taskName {
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

func SetWindowOption(windowID, option, value string) error {
	cmd := exec.Command("tmux", "set-option", "-w", "-t", windowID, "@"+option, value)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("setting @%s on %s: %s: %w", option, windowID, strings.TrimSpace(string(out)), err)
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
