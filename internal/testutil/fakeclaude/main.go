// Package main implements a fake Claude Code binary for integration testing.
// It accepts the same CLI flags as the real Claude, writes a manifest file
// so tests can verify the launch arguments, creates a minimal session file,
// and blocks until SIGINT/SIGTERM.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Manifest struct {
	PID         int       `json:"pid"`
	SessionID   string    `json:"session_id"`
	Name        string    `json:"name"`
	Resume      string    `json:"resume"`
	ForkSession bool      `json:"fork_session"`
	Cwd         string    `json:"cwd"`
	SkipPerms   bool      `json:"skip_permissions"`
	StartedAt   time.Time `json:"started_at"`
}

func main() {
	sessionID := flag.String("session-id", "", "session ID")
	name := flag.String("name", "", "task name")
	resume := flag.String("resume", "", "session to resume")
	forkSession := flag.Bool("fork-session", false, "fork session")
	skipPerms := flag.Bool("dangerously-skip-permissions", false, "skip permissions")
	flag.Parse()

	cwd, _ := os.Getwd()

	effectiveSessionID := *sessionID
	if effectiveSessionID == "" && *resume != "" {
		// Resumed/forked sessions get a new session ID.
		effectiveSessionID = fmt.Sprintf("resumed-%d", os.Getpid())
	}

	manifest := Manifest{
		PID:         os.Getpid(),
		SessionID:   effectiveSessionID,
		Name:        *name,
		Resume:      *resume,
		ForkSession: *forkSession,
		Cwd:         cwd,
		SkipPerms:   *skipPerms,
		StartedAt:   time.Now(),
	}

	// Write manifest so tests can inspect launch arguments.
	controlDir := os.Getenv("FAKECLAUDE_CONTROLDIR")
	if controlDir != "" {
		data, _ := json.MarshalIndent(manifest, "", "  ")
		manifestPath := filepath.Join(controlDir, fmt.Sprintf("%d.json", os.Getpid()))
		_ = os.WriteFile(manifestPath, data, 0o644)
	}

	// Create a minimal session file so findSessionCwd and CopySessionFiles work.
	if effectiveSessionID != "" {
		home, err := os.UserHomeDir()
		if err == nil {
			encodedCwd := encodePath(cwd)
			projectDir := filepath.Join(home, ".claude", "projects", encodedCwd)
			_ = os.MkdirAll(projectDir, 0o755)
			sessionFile := filepath.Join(projectDir, effectiveSessionID+".jsonl")
			_ = os.WriteFile(sessionFile, []byte(`{"type":"init"}`+"\n"), 0o644)
		}
	}

	fmt.Printf("fakeclaude: ready (session=%s, pid=%d)\n", effectiveSessionID, os.Getpid())

	// Block until signaled.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("fakeclaude: exiting")
}

func encodePath(path string) string {
	var b strings.Builder
	for _, r := range path {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
