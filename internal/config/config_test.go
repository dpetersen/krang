package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsHelpfulError(t *testing.T) {
	_, err := Load("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if got := err.Error(); !contains(got, "config not found") || !contains(got, "krang setup") {
		t.Errorf("error should mention 'config not found' and 'krang setup', got: %s", got)
	}
}

func TestLoadValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"sandbox_command": "sandvault run"}`), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SandboxCommand != "sandvault run" {
		t.Errorf("expected 'sandvault run', got %q", cfg.SandboxCommand)
	}
}

func TestLoadEmptySandboxCommandAllowed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"sandbox_command": ""}`), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SandboxCommand != "" {
		t.Errorf("expected empty string, got %q", cfg.SandboxCommand)
	}
}

func TestLoadMissingSandboxFieldDefaultsToEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{}`), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SandboxCommand != "" {
		t.Errorf("expected empty string for missing field, got %q", cfg.SandboxCommand)
	}
}

func TestLoadMalformedJSONReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{not json`), 0o644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestPathEnvVarOverride(t *testing.T) {
	t.Setenv("KRANG_CONFIG", "/custom/path/config.json")
	if got := Path(); got != "/custom/path/config.json" {
		t.Errorf("expected env var path, got %q", got)
	}
}

func TestPathDefaultFallback(t *testing.T) {
	t.Setenv("KRANG_CONFIG", "")
	got := Path()
	if !contains(got, filepath.Join(".config", "krang", "config.json")) {
		t.Errorf("expected default path containing .config/krang/config.json, got %q", got)
	}
}

func TestWriteAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")

	cfg := Config{SandboxCommand: "safehouse --append-profile foo.sb"}
	if err := Write(path, cfg); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Write failed: %v", err)
	}
	if loaded.SandboxCommand != cfg.SandboxCommand {
		t.Errorf("round-trip mismatch: wrote %q, loaded %q", cfg.SandboxCommand, loaded.SandboxCommand)
	}
}

func TestWindowColorDefaults(t *testing.T) {
	cfg := Config{}
	if got := cfg.WindowColor("permission"); got != "red" {
		t.Errorf("expected default 'red' for permission, got %q", got)
	}
	if got := cfg.WindowColor("waiting"); got != "" {
		t.Errorf("expected empty string for waiting by default, got %q", got)
	}
}

func TestWindowColorCustomOverrides(t *testing.T) {
	cfg := Config{
		WindowColorPermission: "colour196",
		WindowColorWaiting:    "#ffaa00",
	}
	if got := cfg.WindowColor("permission"); got != "colour196" {
		t.Errorf("expected 'colour196', got %q", got)
	}
	if got := cfg.WindowColor("waiting"); got != "#ffaa00" {
		t.Errorf("expected '#ffaa00', got %q", got)
	}
}

func TestWindowColorUnstyledStates(t *testing.T) {
	cfg := Config{}
	for _, state := range []string{"ok", "done", "error", ""} {
		if got := cfg.WindowColor(state); got != "" {
			t.Errorf("expected empty string for state %q, got %q", state, got)
		}
	}
}

func TestClassifyAttentionEnabledByDefault(t *testing.T) {
	cfg := Config{}
	if !cfg.ClassifyAttentionEnabled() {
		t.Error("expected classify attention enabled by default (nil pointer)")
	}
}

func TestClassifyAttentionEnabled(t *testing.T) {
	enabled := true
	cfg := Config{ClassifyAttention: &enabled}
	if !cfg.ClassifyAttentionEnabled() {
		t.Error("expected classify attention enabled")
	}
}

func TestClassifyAttentionExplicitlyDisabled(t *testing.T) {
	disabled := false
	cfg := Config{ClassifyAttention: &disabled}
	if cfg.ClassifyAttentionEnabled() {
		t.Error("expected classify attention disabled")
	}
}

func TestWindowColorsEnabledByDefault(t *testing.T) {
	cfg := Config{}
	if !cfg.WindowColorsActive() {
		t.Error("expected window colors enabled by default (nil pointer)")
	}
}

func TestWindowColorsDisabled(t *testing.T) {
	disabled := false
	cfg := Config{WindowColorsEnabled: &disabled}
	if cfg.WindowColorsActive() {
		t.Error("expected window colors disabled")
	}
	if got := cfg.WindowColor("permission"); got != "" {
		t.Errorf("expected empty string when disabled, got %q", got)
	}
	if got := cfg.WindowColor("waiting"); got != "" {
		t.Errorf("expected empty string when disabled, got %q", got)
	}
}

func TestWindowColorsExplicitlyEnabled(t *testing.T) {
	enabled := true
	cfg := Config{WindowColorsEnabled: &enabled}
	if !cfg.WindowColorsActive() {
		t.Error("expected window colors enabled")
	}
	if got := cfg.WindowColor("permission"); got != "red" {
		t.Errorf("expected 'red', got %q", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
