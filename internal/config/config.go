package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	SandboxCommand string `json:"sandbox_command"`
}

// Path returns the resolved config file path, checking
// KRANG_CONFIG env var first, then falling back to
// ~/.config/krang/config.json.
func Path() string {
	if p := os.Getenv("KRANG_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "krang", "config.json")
	}
	return filepath.Join(home, ".config", "krang", "config.json")
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
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config at %s: %w", path, err)
	}
	return cfg, nil
}

// Write marshals the config to indented JSON and writes it to path,
// creating parent directories as needed.
func Write(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
