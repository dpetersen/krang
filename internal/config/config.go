package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

type SandboxProfile struct {
	Type    string `yaml:"type"`
	Command string `yaml:"command,omitempty"`
}

type Config struct {
	Sandboxes             map[string]SandboxProfile `yaml:"sandboxes,omitempty"`
	DefaultSandbox        string                    `yaml:"default_sandbox,omitempty"`
	Theme                 string                    `yaml:"theme,omitempty"`
	DefaultVCS            string                    `yaml:"default_vcs,omitempty"`
	GitHubOrgs            []string                  `yaml:"github_orgs,omitempty"`
	ClassifyAttention     *bool                     `yaml:"classify_attention,omitempty"`
	WindowColorsEnabled   *bool                     `yaml:"window_colors_enabled,omitempty"`
	WindowColorPermission string                    `yaml:"window_color_permission,omitempty"`
	WindowColorWaiting    string                    `yaml:"window_color_waiting,omitempty"`
	CCUsageVersion        string                    `yaml:"ccusage_version,omitempty"`
}

const (
	DefaultColorPermission = "red"
	DefaultColorWaiting    = "yellow"
)

func (c Config) Validate() error {
	for name, profile := range c.Sandboxes {
		if profile.Type != "command" {
			return fmt.Errorf("sandbox %q has unknown type %q (supported: command)", name, profile.Type)
		}
	}
	if c.DefaultSandbox != "" {
		if _, ok := c.Sandboxes[c.DefaultSandbox]; !ok {
			return fmt.Errorf("default_sandbox %q not found in sandboxes", c.DefaultSandbox)
		}
	}
	return nil
}

func (c Config) SandboxProfileNames() []string {
	names := make([]string, 0, len(c.Sandboxes))
	for name := range c.Sandboxes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c Config) ResolveSandboxCommand(profileName string) string {
	name := profileName
	if name == "" {
		name = c.DefaultSandbox
	}
	if name == "" {
		return ""
	}
	profile, ok := c.Sandboxes[name]
	if !ok || profile.Type != "command" {
		return ""
	}
	return profile.Command
}

func (c Config) ClassifyAttentionEnabled() bool {
	return c.ClassifyAttention == nil || *c.ClassifyAttention
}

func (c Config) WindowColorsActive() bool {
	return c.WindowColorsEnabled == nil || *c.WindowColorsEnabled
}

func (c Config) WindowColor(attention string) string {
	if !c.WindowColorsActive() {
		return ""
	}
	switch attention {
	case "permission":
		if c.WindowColorPermission != "" {
			return c.WindowColorPermission
		}
		return DefaultColorPermission
	case "waiting":
		if c.WindowColorWaiting != "" {
			return c.WindowColorWaiting
		}
		return ""
	default:
		return ""
	}
}

// Path returns the resolved config file path, checking
// KRANG_CONFIG env var first, then falling back to
// ~/.config/krang/config.yaml.
func Path() string {
	if p := os.Getenv("KRANG_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "krang", "config.yaml")
	}
	return filepath.Join(home, ".config", "krang", "config.yaml")
}

// Load reads and parses the config file at the given path.
// Returns an error if the file doesn't exist (directing the user
// to run 'krang setup') or if the JSON is malformed.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf("config not found at %s; run 'krang setup'", path)
		}
		return Config{}, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config at %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config at %s: %w", path, err)
	}
	return cfg, nil
}

// Write marshals the config to YAML and writes it to path,
// creating parent directories as needed.
func Write(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
