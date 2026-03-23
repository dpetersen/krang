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

// InstanceDir returns the per-instance config directory for a given
// working directory.
func InstanceDir(cwd string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".config", "krang", "instances", EncodePath(cwd))
}

// StateFilePath returns the path to the krang state file for a given
// working directory.
func StateFilePath(cwd string) string {
	return filepath.Join(InstanceDir(cwd), "krang-state.json")
}
