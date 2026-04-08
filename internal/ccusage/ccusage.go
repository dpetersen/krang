package ccusage

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

const DefaultVersion = "18.0.10"

// SessionCost holds cost data for a single Claude Code session
// as reported by ccusage.
type SessionCost struct {
	TotalCost float64
}

// sessionIDOutput is the JSON shape returned by `ccusage session --id <id> --json`.
// This is a flat object with session-level fields, not the sessions/totals
// wrapper returned by `ccusage session --json` (without --id).
type sessionIDOutput struct {
	SessionID   string  `json:"sessionId"`
	TotalCost   float64 `json:"totalCost"`
	TotalTokens int     `json:"totalTokens"`
}

// NpxAvailable reports whether npx is on PATH.
func NpxAvailable() bool {
	_, err := exec.LookPath("npx")
	return err == nil
}

// FetchSessionCost shells out to npx ccusage to get the cost for a
// specific session. Returns nil with no error if npx is unavailable
// or the session has no cost data.
func FetchSessionCost(sessionID, version string) (*SessionCost, error) {
	if !NpxAvailable() {
		return nil, nil
	}
	if version == "" {
		version = DefaultVersion
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pkg := fmt.Sprintf("ccusage@%s", version)
	cmd := exec.CommandContext(ctx, "npx", "--yes", pkg, "session", "--id", sessionID, "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ccusage: %w", err)
	}

	var result sessionIDOutput
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("ccusage: parsing output: %w", err)
	}

	if result.SessionID == "" {
		return nil, nil
	}

	return &SessionCost{
		TotalCost: result.TotalCost,
	}, nil
}
