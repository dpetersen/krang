package classify

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type Result struct {
	NeedsAttention bool `json:"needs_attention"`
}

const jsonSchema = `{"type":"object","properties":{"needs_attention":{"type":"boolean","description":"true if Claude is asking the user a question or needs human input to proceed, false if Claude finished its work"}},"required":["needs_attention"]}`

func Classify(taskName, lastAssistantMessage, processContext string) (*Result, error) {
	promptText := fmt.Sprintf(`You are classifying the final message from a Claude Code session named %q.

Determine whether Claude is asking the user a question or waiting for human input (needs_attention=true), or whether Claude has finished its work and is idle (needs_attention=false).

Signs of needing attention: asking a question, requesting confirmation, presenting options to choose from, asking for clarification, suggesting next steps and asking which to pursue.

Signs of being done: summarizing completed work, reporting results, no question or request for input in the final sentences, purely informational output.

Last assistant message:
---
%s
---`, taskName, lastAssistantMessage)

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

	var envelope struct {
		StructuredOutput Result `json:"structured_output"`
		Result           string `json:"result"`
		IsError          bool   `json:"is_error"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return nil, fmt.Errorf("parsing classify JSON: %w (raw: %s)", err, strings.TrimSpace(string(out)))
	}

	if envelope.IsError {
		return nil, fmt.Errorf("claude -p returned error: %s", envelope.Result)
	}

	return &envelope.StructuredOutput, nil
}
