package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const hookURL = "http://127.0.0.1:19283/hooks/event"

var hookedEvents = []string{
	"SessionStart",
	"Stop",
	"PermissionRequest",
	"TaskCompleted",
	"StopFailure",
	"Notification",
	"SessionEnd",
}

type hookEntry struct {
	Type    string `json:"type"`
	URL     string `json:"url"`
	Timeout int    `json:"timeout"`
}

type hookMatcher struct {
	Matcher string      `json:"matcher,omitempty"`
	Hooks   []hookEntry `json:"hooks"`
}

func Install() error {
	settingsPath, err := claudeSettingsPath()
	if err != nil {
		return err
	}

	settings, err := readSettings(settingsPath)
	if err != nil {
		return err
	}

	hooksMap, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooksMap = make(map[string]any)
	}

	krangHook := hookEntry{
		Type:    "http",
		URL:     hookURL,
		Timeout: 5,
	}

	for _, event := range hookedEvents {
		matcher := hookMatcher{
			Hooks: []hookEntry{krangHook},
		}
		if event == "Notification" {
			matcher.Matcher = "permission_prompt|idle_prompt"
		}

		existing, ok := hooksMap[event].([]any)
		if !ok {
			existing = nil
		}

		alreadyInstalled := false
		for _, entry := range existing {
			if entryMap, ok := entry.(map[string]any); ok {
				if hooks, ok := entryMap["hooks"].([]any); ok {
					for _, h := range hooks {
						if hMap, ok := h.(map[string]any); ok {
							if hMap["url"] == hookURL {
								alreadyInstalled = true
								break
							}
						}
					}
				}
			}
		}

		if !alreadyInstalled {
			existing = append(existing, matcher)
			hooksMap[event] = existing
		}
	}

	settings["hooks"] = hooksMap
	return writeSettings(settingsPath, settings)
}

func Uninstall() error {
	settingsPath, err := claudeSettingsPath()
	if err != nil {
		return err
	}

	settings, err := readSettings(settingsPath)
	if err != nil {
		return err
	}

	hooksMap, ok := settings["hooks"].(map[string]any)
	if !ok {
		return nil
	}

	for _, event := range hookedEvents {
		entries, ok := hooksMap[event].([]any)
		if !ok {
			continue
		}

		var filtered []any
		for _, entry := range entries {
			isKrang := false
			if entryMap, ok := entry.(map[string]any); ok {
				if hooks, ok := entryMap["hooks"].([]any); ok {
					for _, h := range hooks {
						if hMap, ok := h.(map[string]any); ok {
							if hMap["url"] == hookURL {
								isKrang = true
								break
							}
						}
					}
				}
			}
			if !isKrang {
				filtered = append(filtered, entry)
			}
		}

		if len(filtered) == 0 {
			delete(hooksMap, event)
		} else {
			hooksMap[event] = filtered
		}
	}

	if len(hooksMap) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooksMap
	}

	return writeSettings(settingsPath, settings)
}

func claudeSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func readSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]any), nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return settings, nil
}

func writeSettings(path string, settings map[string]any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
