package pathutil

import (
	"strings"
	"testing"
)

func TestEncodePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/Users/alice/dev/krang", "-Users-alice-dev-krang"},
		{"/home/bob/code/my-project", "-home-bob-code-my-project"},
		{"simple", "simple"},
		{"/a/b", "-a-b"},
	}
	for _, tt := range tests {
		got := EncodePath(tt.input)
		if got != tt.want {
			t.Errorf("EncodePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestInstanceID(t *testing.T) {
	id := InstanceID("/Users/alice/dev/krang")
	if id == "" {
		t.Fatal("InstanceID returned empty string")
	}

	// Should start with basename.
	if id[:5] != "krang" {
		t.Errorf("InstanceID should start with basename, got %q", id)
	}

	// Should contain a hyphen followed by 4 hex chars.
	if len(id) != len("krang")+1+4 {
		t.Errorf("InstanceID unexpected length: %q (len %d)", id, len(id))
	}

	// Same input should produce the same ID.
	id2 := InstanceID("/Users/alice/dev/krang")
	if id != id2 {
		t.Errorf("InstanceID not deterministic: %q != %q", id, id2)
	}

	// Different paths with same basename should differ.
	id3 := InstanceID("/Users/bob/dev/krang")
	if id == id3 {
		t.Errorf("InstanceID collision: %q == %q for different paths", id, id3)
	}
}

func TestStateFilePath(t *testing.T) {
	path := StateFilePath("/Users/alice/dev/krang")
	if path == "" {
		t.Fatal("StateFilePath returned empty string")
	}

	// Should end with krang-state.json.
	suffix := "krang-state.json"
	if path[len(path)-len(suffix):] != suffix {
		t.Errorf("StateFilePath should end with %q, got %q", suffix, path)
	}

	// Should be under .local/state.
	if !strings.Contains(path, ".local/state/krang") {
		t.Errorf("StateFilePath should be under .local/state, got %q", path)
	}
}

func TestDataDir(t *testing.T) {
	dir := DataDir("/Users/alice/dev/krang")
	if dir == "" {
		t.Fatal("DataDir returned empty string")
	}

	// Should be under .local/share.
	if !strings.Contains(dir, ".local/share/krang") {
		t.Errorf("DataDir should be under .local/share, got %q", dir)
	}
}
