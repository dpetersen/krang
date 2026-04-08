package usage

import (
	"os"
	"path/filepath"
	"testing"
)

func writeJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, line := range lines {
		f.WriteString(line + "\n")
	}
}

func TestParseTranscriptBasic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	writeJSONL(t, path,
		`{"type":"user","timestamp":"2025-01-15T10:00:00Z","message":{"content":"hello"}}`,
		`{"type":"assistant","timestamp":"2025-01-15T10:00:01Z","message":{"id":"msg_001","model":"claude-opus-4-6","usage":{"input_tokens":1000,"output_tokens":200,"cache_creation_input_tokens":5000,"cache_read_input_tokens":10000}}}`,
		`{"type":"assistant","timestamp":"2025-01-15T10:00:05Z","message":{"id":"msg_002","model":"claude-haiku-4-5-20251001","usage":{"input_tokens":500,"output_tokens":100,"cache_creation_input_tokens":0,"cache_read_input_tokens":2000}}}`,
	)

	summary, err := ParseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(summary.Snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(summary.Snapshots))
	}

	if len(summary.TotalByModel) != 2 {
		t.Fatalf("expected 2 models, got %d", len(summary.TotalByModel))
	}

	opus := summary.TotalByModel["claude-opus-4-6"]
	if opus.Input != 1000 || opus.Output != 200 {
		t.Errorf("opus tokens: got input=%d output=%d", opus.Input, opus.Output)
	}
	haiku := summary.TotalByModel["claude-haiku-4-5-20251001"]
	if haiku.Input != 500 || haiku.Output != 100 {
		t.Errorf("haiku tokens: got input=%d output=%d", haiku.Input, haiku.Output)
	}

	if !summary.Snapshots[0].Timestamp.Before(summary.Snapshots[1].Timestamp) {
		t.Error("snapshots should be chronologically sorted")
	}
}

func TestParseTranscriptDeduplicatesByMessageID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	// Same message ID appears 3 times (streaming updates). Only the last should count.
	writeJSONL(t, path,
		`{"type":"assistant","timestamp":"2025-01-15T10:00:01Z","message":{"id":"msg_001","model":"claude-opus-4-6","usage":{"input_tokens":100,"output_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":5000}}}`,
		`{"type":"assistant","timestamp":"2025-01-15T10:00:01Z","message":{"id":"msg_001","model":"claude-opus-4-6","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":5000}}}`,
		`{"type":"assistant","timestamp":"2025-01-15T10:00:01Z","message":{"id":"msg_001","model":"claude-opus-4-6","usage":{"input_tokens":100,"output_tokens":200,"cache_creation_input_tokens":0,"cache_read_input_tokens":5000}}}`,
	)

	summary, err := ParseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(summary.Snapshots) != 1 {
		t.Fatalf("expected 1 snapshot after dedup, got %d", len(summary.Snapshots))
	}

	opus := summary.TotalByModel["claude-opus-4-6"]
	if opus.Output != 200 {
		t.Errorf("expected output=200 (last entry), got %d", opus.Output)
	}
}

func TestParseTranscriptSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	writeJSONL(t, path,
		`not json at all`,
		`{"type":"assistant","timestamp":"2025-01-15T10:00:01Z","message":{"id":"msg_001","model":"claude-opus-4-6","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
	)

	summary, err := ParseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(summary.Snapshots))
	}
}

func TestParseTranscriptWithSubagents(t *testing.T) {
	dir := t.TempDir()
	// Real layout: <session-id>.jsonl alongside <session-id>/subagents/*.jsonl
	sessionID := "abc123-def456"
	os.MkdirAll(filepath.Join(dir, sessionID, "subagents"), 0o755)

	mainPath := filepath.Join(dir, sessionID+".jsonl")
	writeJSONL(t, mainPath,
		`{"type":"assistant","timestamp":"2025-01-15T10:00:01Z","message":{"id":"msg_001","model":"claude-opus-4-6","usage":{"input_tokens":1000,"output_tokens":200,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
	)

	subPath := filepath.Join(dir, sessionID, "subagents", "agent-xyz.jsonl")
	writeJSONL(t, subPath,
		`{"type":"assistant","timestamp":"2025-01-15T10:00:03Z","message":{"id":"msg_sub_001","model":"claude-haiku-4-5-20251001","usage":{"input_tokens":500,"output_tokens":100,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
	)

	summary, err := ParseTranscript(mainPath)
	if err != nil {
		t.Fatal(err)
	}

	if len(summary.Snapshots) != 2 {
		t.Fatalf("expected 2 snapshots (main + subagent), got %d", len(summary.Snapshots))
	}
	if len(summary.TotalByModel) != 2 {
		t.Fatalf("expected 2 models, got %d", len(summary.TotalByModel))
	}
}

func TestParseTranscriptSkipsZeroUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	writeJSONL(t, path,
		`{"type":"assistant","timestamp":"2025-01-15T10:00:01Z","message":{"id":"msg_001","model":"claude-opus-4-6","usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
		`{"type":"assistant","timestamp":"2025-01-15T10:00:02Z","message":{"id":"msg_002","model":"claude-opus-4-6","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
	)

	summary, err := ParseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Snapshots) != 1 {
		t.Fatalf("expected 1 snapshot (zero-usage skipped), got %d", len(summary.Snapshots))
	}
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{500, "500"},
		{1500, "2K"},
		{48000, "48K"},
		{1_200_000, "1.2M"},
		{20_000_000, "20.0M"},
	}
	for _, tt := range tests {
		got := FormatTokenCount(tt.input)
		if got != tt.expected {
			t.Errorf("FormatTokenCount(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
