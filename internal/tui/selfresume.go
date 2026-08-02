package tui

import (
	"time"

	"github.com/dpetersen/krang/internal/hooks"
)

// Tools that hand control back to the prompt while arranging for Claude
// to pick the conversation up again with no user input. A task in this
// state looks idle to every other signal krang has, which is exactly
// backwards: it is waiting on something, not on you.
const (
	scheduleWakeupTool = "ScheduleWakeup"
	monitorTool        = "Monitor"
)

const (
	// Wakeups and monitor timeouts fire a little late. Hold the marker
	// slightly past the deadline rather than dropping it early.
	selfResumeSlack = 30 * time.Second

	// A persistent monitor runs until the session ends, so there is no
	// deadline to read. Cap it so a task cannot advertise a pending
	// self-resume indefinitely.
	persistentMonitorHorizon = 6 * time.Hour
)

// selfResumeDeadline reports when a tool call's self-resume mechanism is
// expected to have lapsed, and whether the call arms one at all.
//
// Claude Code emits no hook event when a monitor ends or a wakeup fires,
// so the deadline is the only thing keeping the marker from going stale.
// It is derived from the tool's own arguments, which arrive intact in
// the PostToolUse payload.
func selfResumeDeadline(event hooks.HookEvent, now time.Time) (time.Time, bool) {
	switch event.ToolName {
	case scheduleWakeupTool:
		// A stop call cancels the loop rather than arming it.
		if event.ToolInput.Stop || event.ToolInput.DelaySeconds <= 0 {
			return time.Time{}, false
		}
		delay := time.Duration(event.ToolInput.DelaySeconds) * time.Second
		return now.Add(delay + selfResumeSlack), true

	case monitorTool:
		if event.ToolInput.Persistent {
			return now.Add(persistentMonitorHorizon), true
		}
		if event.ToolInput.TimeoutMS <= 0 {
			return time.Time{}, false
		}
		timeout := time.Duration(event.ToolInput.TimeoutMS) * time.Millisecond
		return now.Add(timeout + selfResumeSlack), true
	}
	return time.Time{}, false
}

// selfResuming reports whether the task is expected to carry on without
// the user, so the idle-looking attention states can say so.
func (m Model) selfResuming(taskID string) bool {
	until, ok := m.selfResume[taskID]
	return ok && time.Now().Before(until)
}
