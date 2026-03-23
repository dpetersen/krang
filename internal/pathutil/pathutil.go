package pathutil

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EncodePath encodes a path the same way Claude does for project
// directory names: replace all non-alphanumeric chars with '-'.
func EncodePath(path string) string {
	var b strings.Builder
	for _, r := range path {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// InstanceID returns a short identifier for a working directory,
// suitable for use in session names and paths. Format: <basename>-<4 hex SHA-256>.
func InstanceID(cwd string) string {
	basename := filepath.Base(cwd)
	hash := sha256.Sum256([]byte(cwd))
	return fmt.Sprintf("%s-%x", basename, hash[:2])
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return home
}

// DataDir returns the per-instance data directory (for persistent
// storage like the SQLite database). Uses XDG_DATA_HOME (~/.local/share).
func DataDir(cwd string) string {
	return filepath.Join(homeDir(), ".local", "share", "krang", "instances", EncodePath(cwd))
}

// StateDir returns the per-instance state directory (for ephemeral
// runtime files like the port state file). Uses XDG_STATE_HOME (~/.local/state).
func StateDir(cwd string) string {
	return filepath.Join(homeDir(), ".local", "state", "krang", "instances", EncodePath(cwd))
}

// StateFilePath returns the path to the krang state file for a given
// working directory.
func StateFilePath(cwd string) string {
	return filepath.Join(StateDir(cwd), "krang-state.json")
}
