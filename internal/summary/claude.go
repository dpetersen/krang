package summary

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type SummaryResult struct {
	OneLiner string `json:"one_liner"`
	Phase    string `json:"phase"`
}

const jsonSchema = `{"type":"object","properties":{"one_liner":{"type":"string","description":"Under 60 chars summarizing the TOPIC or WORK being done, not the UI state"},"phase":{"type":"string","enum":["planning","coding","testing","debugging","waiting","error","done"]}},"required":["one_liner","phase"]}`

func Summarize(taskName, paneContent, processContext string) (*SummaryResult, error) {
	promptText := fmt.Sprintf(`This is terminal output from a Claude Code session named %q running in tmux. Summarize what TOPIC or WORK this session is about in under 60 characters. Focus on the subject matter being discussed or the code being written, NOT the UI state (don't say "waiting for input" or "idle" — I already know that from other signals).

Terminal output:
---
%s
---`, taskName, paneContent)

	if processContext != "" {
		promptText += "\n\n" + processContext
	}

	cmd := exec.Command(
		"claude",
		"-p",
		"--model", "haiku",
		"--output-format", "json",
		"--json-schema", jsonSchema,
		"--no-session-persistence",
		promptText,
	)
	cmd.Stdin = nil

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("claude -p failed: %s: %w", strings.TrimSpace(string(exitErr.Stderr)), err)
		}
		return nil, fmt.Errorf("running claude -p: %w", err)
	}

	// claude -p --output-format json wraps structured output in an
	// envelope with the actual data under "structured_output".
	var envelope struct {
		StructuredOutput SummaryResult `json:"structured_output"`
		Result           string        `json:"result"`
		IsError          bool          `json:"is_error"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return nil, fmt.Errorf("parsing summary JSON: %w (raw: %s)", err, strings.TrimSpace(string(out)))
	}

	if envelope.IsError {
		return nil, fmt.Errorf("claude -p returned error: %s", envelope.Result)
	}

	return &envelope.StructuredOutput, nil
}
