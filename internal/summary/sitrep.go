package summary

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/proctree"
	"github.com/dpetersen/krang/internal/tmux"
)

type SitRepInput struct {
	Tasks      []db.Task
	Processes  map[string]*proctree.TaskProcesses
	ScreenRows int
	ScreenCols int
}

func GenerateSitRep(input SitRepInput) (string, error) {
	var taskDescriptions []string

	for _, t := range input.Tasks {
		if t.State != db.StateActive {
			continue
		}

		desc := fmt.Sprintf("## Task: %s\n- Attention: %s\n- Working directory: %s\n- Summary: %s",
			t.Name, t.Attention, t.Cwd, t.Summary)

		if t.TranscriptPath != "" {
			desc += fmt.Sprintf("\n- Transcript file: %s (read this for context)", t.TranscriptPath)
		}

		if t.TmuxWindow != "" {
			pane, err := tmux.CapturePane(t.TmuxWindow, 30)
			if err == nil {
				stripped := strings.TrimSpace(StripANSI(pane))
				if stripped != "" {
					desc += fmt.Sprintf("\n- Current terminal (last 30 lines):\n```\n%s\n```", stripped)
				}
			}
		}

		if procCtx := proctree.FormatForPrompt(input.Processes[t.ID]); procCtx != "" {
			desc += "\n- " + procCtx
		}

		taskDescriptions = append(taskDescriptions, desc)
	}

	if len(taskDescriptions) == 0 {
		return "No active tasks.", nil
	}

	prompt := fmt.Sprintf(`You are briefing a developer who has been away and needs to catch up on their active Claude Code sessions. They have %d active tasks.

For each task, I've provided metadata and the transcript file path. Read each transcript to understand the full context of what's been happening. Focus on the LAST several messages to understand where things currently stand, but read earlier if needed to understand the journey.

Your briefing should:
- Use markdown formatting (headers, bold, lists, etc.) for readability
- For each task: use a ## header with the task name, then a concise briefing covering the goal, progress so far, current status, and what needs the developer's attention
- If a task is blocked on the developer, call that out clearly
- End with a ## Recommendation section on what to focus on first and why
- Target %d columns wide for line length.

Here are the active tasks:

%s`,
		len(taskDescriptions),
		input.ScreenCols,
		strings.Join(taskDescriptions, "\n\n"),
	)

	cmd := exec.Command(
		"claude",
		"-p",
		"--model", "sonnet",
		"--output-format", "text",
		"--max-budget-usd", "1.00",
		"--allowedTools", "Read",
		"--no-session-persistence",
		prompt,
	)
	cmd.Stdin = nil

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("sit rep failed: %s: %w", strings.TrimSpace(string(exitErr.Stderr)), err)
		}
		return "", fmt.Errorf("running sit rep: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}
