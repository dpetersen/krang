package tui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dpetersen/krang/internal/hooks"
)

func TestSelfResumeDeadlineFromScheduleWakeup(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	event := hooks.HookEvent{
		HookEventName: "PostToolUse",
		ToolName:      "ScheduleWakeup",
		ToolInput:     hooks.ToolInput{DelaySeconds: 270},
	}

	until, armed := selfResumeDeadline(event, now)
	if !armed {
		t.Fatal("want armed for a ScheduleWakeup call")
	}
	if want := now.Add(270*time.Second + selfResumeSlack); !until.Equal(want) {
		t.Errorf("deadline = %v, want %v", until, want)
	}
}

// ScheduleWakeup{stop: true} ends the loop rather than arming it.
func TestSelfResumeDeadlineScheduleWakeupStop(t *testing.T) {
	event := hooks.HookEvent{
		HookEventName: "PostToolUse",
		ToolName:      "ScheduleWakeup",
		ToolInput:     hooks.ToolInput{Stop: true},
	}

	if _, armed := selfResumeDeadline(event, time.Now()); armed {
		t.Error("want not armed when the wakeup loop is being stopped")
	}
}

func TestSelfResumeDeadlineFromMonitorTimeout(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	event := hooks.HookEvent{
		HookEventName: "PostToolUse",
		ToolName:      "Monitor",
		ToolInput:     hooks.ToolInput{TimeoutMS: 300_000},
	}

	until, armed := selfResumeDeadline(event, now)
	if !armed {
		t.Fatal("want armed for a Monitor call")
	}
	if want := now.Add(300*time.Second + selfResumeSlack); !until.Equal(want) {
		t.Errorf("deadline = %v, want %v", until, want)
	}
}

func TestSelfResumeDeadlinePersistentMonitor(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	event := hooks.HookEvent{
		HookEventName: "PostToolUse",
		ToolName:      "Monitor",
		ToolInput:     hooks.ToolInput{Persistent: true, TimeoutMS: 300_000},
	}

	until, armed := selfResumeDeadline(event, now)
	if !armed {
		t.Fatal("want armed for a persistent Monitor")
	}
	if want := now.Add(persistentMonitorHorizon); !until.Equal(want) {
		t.Errorf("deadline = %v, want the persistent horizon %v", until, want)
	}
}

func TestSelfResumeDeadlineIgnoresOtherTools(t *testing.T) {
	for _, tool := range []string{"Bash", "Edit", "Read", ""} {
		event := hooks.HookEvent{HookEventName: "PostToolUse", ToolName: tool}
		if _, armed := selfResumeDeadline(event, time.Now()); armed {
			t.Errorf("tool %q: want not armed", tool)
		}
	}
}

func TestSelfResumingExpires(t *testing.T) {
	m := Model{selfResume: map[string]time.Time{
		"live":    time.Now().Add(time.Minute),
		"expired": time.Now().Add(-time.Minute),
	}}

	if !m.selfResuming("live") {
		t.Error("want live task to report self-resuming")
	}
	if m.selfResuming("expired") {
		t.Error("want expired task to stop reporting self-resuming")
	}
	if m.selfResuming("unknown") {
		t.Error("want unknown task to report not self-resuming")
	}
}

// Decoded from a payload krang actually received, captured from the
// events table while a Monitor call was in flight. Guards against the
// hook payload shape drifting out from under the ToolInput struct.
func TestToolInputDecodesRealMonitorPayload(t *testing.T) {
	const payload = `{
		"session_id": "adcd39f1-2ab4-46c5-8af2-bdf530d84a31",
		"hook_event_name": "PostToolUse",
		"tool_name": "Monitor",
		"tool_input": {
			"description": "krang hook-event probe",
			"timeout_ms": 30000,
			"persistent": false,
			"command": "echo hi"
		},
		"tool_use_id": "toolu_01RMe8VxxP7gb9Sg2BBQg1qR",
		"duration_ms": 12
	}`

	var event hooks.HookEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("decoding payload: %v", err)
	}

	if event.ToolName != "Monitor" {
		t.Errorf("ToolName = %q, want Monitor", event.ToolName)
	}
	if event.ToolInput.TimeoutMS != 30000 {
		t.Errorf("TimeoutMS = %v, want 30000", event.ToolInput.TimeoutMS)
	}
	if event.ToolInput.Persistent {
		t.Error("Persistent = true, want false")
	}
	if _, armed := selfResumeDeadline(event, time.Now()); !armed {
		t.Error("want the real payload to arm a self-resume marker")
	}
}
