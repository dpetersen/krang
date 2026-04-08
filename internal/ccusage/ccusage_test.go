package ccusage

import (
	"encoding/json"
	"testing"
)

func TestParseSessionIDOutput(t *testing.T) {
	raw := `{
		"sessionId": "abc-123",
		"totalCost": 2.76,
		"totalTokens": 2448473,
		"entries": [
			{
				"timestamp": "2026-04-02T19:02:01.161Z",
				"inputTokens": 3,
				"outputTokens": 30,
				"model": "claude-opus-4-6",
				"costUSD": 0
			}
		]
	}`

	var result sessionIDOutput
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}

	if result.SessionID != "abc-123" {
		t.Errorf("session ID = %q, want %q", result.SessionID, "abc-123")
	}
	if result.TotalCost != 2.76 {
		t.Errorf("total cost = %f, want 2.76", result.TotalCost)
	}
	if result.TotalTokens != 2448473 {
		t.Errorf("total tokens = %d, want 2448473", result.TotalTokens)
	}
}

func TestParseEmptySessionIDOutput(t *testing.T) {
	raw := `{}`

	var result sessionIDOutput
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}

	if result.SessionID != "" {
		t.Errorf("expected empty session ID, got %q", result.SessionID)
	}
}

func TestDefaultVersion(t *testing.T) {
	if DefaultVersion == "" {
		t.Error("DefaultVersion should not be empty")
	}
}
