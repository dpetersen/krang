package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	SandboxCommand        string   `yaml:"sandbox_command"`
	Theme                 string   `yaml:"theme,omitempty"`
	DefaultVCS            string   `yaml:"default_vcs,omitempty"`
	GitHubOrgs            []string `yaml:"github_orgs,omitempty"`
	ClassifyAttention     *bool    `yaml:"classify_attention,omitempty"`
	WindowColorsEnabled   *bool    `yaml:"window_colors_enabled,omitempty"`
	WindowColorPermission string   `yaml:"window_color_permission,omitempty"`
	WindowColorWaiting    string   `yaml:"window_color_waiting,omitempty"`
}

const (
	DefaultColorPermission = "red"
	DefaultColorWaiting    = "yellow"
)

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
