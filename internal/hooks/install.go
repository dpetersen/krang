package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// legacyHookURL is the old hardcoded HTTP hook URL used before
// multi-krang support. Install() removes hooks matching this URL.
const legacyHookURL = "http://127.0.0.1:19283/hooks/event"

var hookedEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"Stop",
	"PermissionRequest",
	"PreToolUse",
	"PostToolUse",
	"PostToolUseFailure",
	"SubagentStart",
	"SubagentStop",
	"TaskCompleted",
	"StopFailure",
	"Notification",
	"SessionEnd",
}

const relayScript = `#!/bin/bash
dbg() { [ -n "$KRANG_DEBUG" ] && echo "[$(date '+%H:%M:%S')] relay: $*" >> /tmp/krang-debug.log; }
[ -z "$KRANG_STATEFILE" ] && exit 0
[ ! -f "$KRANG_STATEFILE" ] && { dbg "statefile not found: $KRANG_STATEFILE"; exit 0; }
PORT=$(jq -r .port "$KRANG_STATEFILE" 2>/dev/null)
[ -z "$PORT" ] && { dbg "empty port from $KRANG_STATEFILE"; exit 0; }
INPUT=$(cat)
dbg "port=$PORT payload=$INPUT"
RESP=$(echo "$INPUT" | curl -s --max-time 5 -o /dev/null -w '%{http_code}' \
  -X POST -H 'Content-Type: application/json' \
  -d @- "http://127.0.0.1:$PORT/hooks/event")
dbg "http_status=$RESP"
exit 0
`

type hookEntry struct {
	Type    string `json:"type"`
	URL     string `json:"url,omitempty"`
	Command string `json:"command,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

type hookMatcher struct {
	Matcher string      `json:"matcher,omitempty"`
	Hooks   []hookEntry `json:"hooks"`
}

func relayScriptPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home dir: %w", err)
	}
	return filepath.Join(home, ".config", "krang", "hooks", "relay.sh"), nil
}

func installRelayScript() (string, error) {
	scriptPath, err := relayScriptPath()
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(scriptPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating hooks dir: %w", err)
	}

	if err := os.WriteFile(scriptPath, []byte(relayScript), 0o755); err != nil {
		return "", fmt.Errorf("writing relay script: %w", err)
	}

	return scriptPath, nil
}

func Install() error {
	scriptPath, err := installRelayScript()
	if err != nil {
		return err
	}

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

	// Migrate: remove any legacy HTTP hooks before installing command hooks.
	removeLegacyHTTPHooks(hooksMap)

	krangHook := hookEntry{
		Type:    "command",
		Command: scriptPath,
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
							if hMap["command"] == scriptPath {
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
	scriptPath, err := relayScriptPath()
	if err != nil {
		return err
	}

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

	// Remove both command hooks (current) and HTTP hooks (legacy).
	for _, event := range hookedEvents {
		entries, ok := hooksMap[event].([]any)
		if !ok {
			continue
		}

		var filtered []any
		for _, entry := range entries {
			if isKrangHookEntry(entry, scriptPath) {
				continue
			}
			filtered = append(filtered, entry)
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

// isKrangHookEntry returns true if the entry is a krang hook — either
// a command hook matching the relay script path, or a legacy HTTP hook.
func isKrangHookEntry(entry any, scriptPath string) bool {
	entryMap, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := entryMap["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hooks {
		hMap, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if hMap["command"] == scriptPath {
			return true
		}
		if hMap["url"] == legacyHookURL {
			return true
		}
	}
	return false
}

// removeLegacyHTTPHooks strips any old HTTP-type krang hooks from the
// hooks map so they don't linger after upgrading to command hooks.
func removeLegacyHTTPHooks(hooksMap map[string]any) {
	for _, event := range hookedEvents {
		entries, ok := hooksMap[event].([]any)
		if !ok {
			continue
		}

		var filtered []any
		for _, entry := range entries {
			if isLegacyHTTPHook(entry) {
				continue
			}
			filtered = append(filtered, entry)
		}

		if len(filtered) == 0 {
			delete(hooksMap, event)
		} else {
			hooksMap[event] = filtered
		}
	}
}

func isLegacyHTTPHook(entry any) bool {
	entryMap, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := entryMap["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hooks {
		hMap, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if hMap["url"] == legacyHookURL {
			return true
		}
	}
	return false
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
