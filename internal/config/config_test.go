package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsHelpfulError(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if got := err.Error(); !contains(got, "config not found") || !contains(got, "krang setup") {
		t.Errorf("error should mention 'config not found' and 'krang setup', got: %s", got)
	}
}

func TestLoadValidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte(`
sandboxes:
  default:
    type: command
    command: sandvault run
default_sandbox: default
`), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Sandboxes["default"].Command != "sandvault run" {
		t.Errorf("expected 'sandvault run', got %q", cfg.Sandboxes["default"].Command)
	}
	if cfg.DefaultSandbox != "default" {
		t.Errorf("expected default_sandbox 'default', got %q", cfg.DefaultSandbox)
	}
}

func TestLoadEmptyConfigAllowed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("{}\n"), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Sandboxes) != 0 {
		t.Errorf("expected no sandboxes, got %d", len(cfg.Sandboxes))
	}
}

func TestLoadMalformedYAMLReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("sandboxes: [invalid\n"), 0o644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

func TestValidateUnknownType(t *testing.T) {
	cfg := Config{
		Sandboxes: map[string]SandboxProfile{
			"bad": {Type: "docker"},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for unknown sandbox type")
	}
}

func TestValidateDefaultSandboxMissing(t *testing.T) {
	cfg := Config{
		Sandboxes: map[string]SandboxProfile{
			"default": {Type: "command", Command: "safehouse"},
		},
		DefaultSandbox: "nonexistent",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing default_sandbox reference")
	}
}

func TestValidateDefaultSandboxNoSandboxes(t *testing.T) {
	cfg := Config{DefaultSandbox: ""}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error for empty config: %v", err)
	}
}

func TestSandboxProfileNames(t *testing.T) {
	cfg := Config{
		Sandboxes: map[string]SandboxProfile{
			"zulu":  {Type: "command"},
			"alpha": {Type: "command"},
			"mike":  {Type: "command"},
		},
	}
	names := cfg.SandboxProfileNames()
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	if names[0] != "alpha" || names[1] != "mike" || names[2] != "zulu" {
		t.Errorf("expected sorted names, got %v", names)
	}
}

func TestSandboxProfileNamesEmpty(t *testing.T) {
	cfg := Config{}
	names := cfg.SandboxProfileNames()
	if len(names) != 0 {
		t.Errorf("expected empty names, got %v", names)
	}
}

func TestResolveSandboxCommandExplicitProfile(t *testing.T) {
	cfg := Config{
		Sandboxes: map[string]SandboxProfile{
			"default": {Type: "command", Command: "safehouse run"},
			"cloud":   {Type: "command", Command: "safehouse run --cloud"},
		},
		DefaultSandbox: "default",
	}
	if got := cfg.ResolveSandboxCommand("cloud"); got != "safehouse run --cloud" {
		t.Errorf("expected cloud command, got %q", got)
	}
}

func TestResolveSandboxCommandFallsBackToDefault(t *testing.T) {
	cfg := Config{
		Sandboxes: map[string]SandboxProfile{
			"default": {Type: "command", Command: "safehouse run"},
		},
		DefaultSandbox: "default",
	}
	if got := cfg.ResolveSandboxCommand(""); got != "safehouse run" {
		t.Errorf("expected default command, got %q", got)
	}
}

func TestResolveSandboxCommandMissingProfile(t *testing.T) {
	cfg := Config{
		Sandboxes: map[string]SandboxProfile{
			"default": {Type: "command", Command: "safehouse run"},
		},
		DefaultSandbox: "default",
	}
	if got := cfg.ResolveSandboxCommand("nonexistent"); got != "" {
		t.Errorf("expected empty string for missing profile, got %q", got)
	}
}

func TestResolveSandboxCommandNoProfiles(t *testing.T) {
	cfg := Config{}
	if got := cfg.ResolveSandboxCommand(""); got != "" {
		t.Errorf("expected empty string with no profiles, got %q", got)
	}
}

func TestResolveSandboxCommandNoneProfile(t *testing.T) {
	cfg := Config{
		Sandboxes: map[string]SandboxProfile{
			"default": {Type: "command", Command: "safehouse run"},
		},
		DefaultSandbox: "default",
	}
	// "none" is not a real profile — it should resolve to empty.
	if got := cfg.ResolveSandboxCommand("none"); got != "" {
		t.Errorf("expected empty string for 'none' profile, got %q", got)
	}
}

func TestPathEnvVarOverride(t *testing.T) {
	t.Setenv("KRANG_CONFIG", "/custom/path/config.yaml")
	if got := Path(); got != "/custom/path/config.yaml" {
		t.Errorf("expected env var path, got %q", got)
	}
}

func TestPathDefaultFallback(t *testing.T) {
	t.Setenv("KRANG_CONFIG", "")
	got := Path()
	if !contains(got, filepath.Join(".config", "krang", "config.yaml")) {
		t.Errorf("expected default path containing .config/krang/config.yaml, got %q", got)
	}
}

func TestWriteAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")

	cfg := Config{
		Sandboxes: map[string]SandboxProfile{
			"default": {Type: "command", Command: "safehouse --append-profile foo.sb"},
		},
		DefaultSandbox: "default",
	}
	if err := Write(path, cfg); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Write failed: %v", err)
	}
	if loaded.Sandboxes["default"].Command != cfg.Sandboxes["default"].Command {
		t.Errorf("round-trip mismatch: wrote %q, loaded %q",
			cfg.Sandboxes["default"].Command, loaded.Sandboxes["default"].Command)
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
