package tmux

import (
	"fmt"
	"os/exec"
	"strings"
)

const ParkedSession = "krang-parked"

func SessionExists(name string) bool {
	err := exec.Command("tmux", "has-session", "-t", name).Run()
	return err == nil
}

func CreateSession(name string) error {
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("creating session %s: %s: %w", name, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func EnsureParkedSession() error {
	if !SessionExists(ParkedSession) {
		return CreateSession(ParkedSession)
	}
	return nil
}

func CurrentSession() (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return "", fmt.Errorf("getting current session: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func SelectWindow(windowID string) error {
	cmd := exec.Command("tmux", "select-window", "-t", windowID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("selecting window %s: %s: %w", windowID, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func InsideTmux() bool {
	cmd := exec.Command("tmux", "display-message", "-p", "#{session_name}")
	return cmd.Run() == nil
}
