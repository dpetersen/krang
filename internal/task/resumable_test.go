package task

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dpetersen/krang/internal/pathutil"
)

// writeSessionFile lays out a Claude transcript the way Claude Code does:
// ~/.claude/projects/<encoded-cwd>/<session-id>.jsonl
func writeSessionFile(t *testing.T, home, cwd, sessionID string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", pathutil.EncodePath(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating project dir: %v", err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("writing session file: %v", err)
	}
	return path
}

func TestSessionResumableInPreferredProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := filepath.Join(home, "code", "proj")
	writeSessionFile(t, home, cwd, "sess-1")

	if !SessionResumable("sess-1", cwd) {
		t.Error("want resumable when the transcript sits in the preferred project dir")
	}
}

func TestSessionResumableInOtherProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeSessionFile(t, home, filepath.Join(home, "code", "elsewhere"), "sess-1")

	if !SessionResumable("sess-1", filepath.Join(home, "code", "proj")) {
		t.Error("want resumable when the transcript sits under a different project dir")
	}
}

// The case this check exists for: Claude deletes transcripts older than
// cleanupPeriodDays, leaving a frozen task holding a session ID that
// resolves to nothing. Krang used to offer resume anyway.
func TestSessionNotResumableAfterTranscriptCleanup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := filepath.Join(home, "code", "proj")
	path := writeSessionFile(t, home, cwd, "sess-1")

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing transcript: %v", err)
	}

	if SessionResumable("sess-1", cwd) {
		t.Error("want not resumable once the transcript is deleted")
	}
}

func TestSessionNotResumableWithoutSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if SessionResumable("", filepath.Join(home, "code", "proj")) {
		t.Error("want not resumable without a session ID")
	}
}
