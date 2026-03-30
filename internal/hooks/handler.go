package hooks

import (
	"github.com/dpetersen/krang/internal/db"
)

// HookEvent represents the JSON payload from Claude Code hooks.
type HookEvent struct {
	SessionID            string `json:"session_id"`
	HookEventName        string `json:"hook_event_name"`
	Cwd                  string `json:"cwd"`
	TranscriptPath       string `json:"transcript_path,omitempty"`
	NotificationType     string `json:"notification_type,omitempty"`
	ToolName             string `json:"tool_name,omitempty"`
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`
	AgentID              string `json:"agent_id,omitempty"`
	AgentType            string `json:"agent_type,omitempty"`
	RawPayload           string `json:"-"`
}

// AttentionFromEvent maps a hook event to the appropriate attention state.
func AttentionFromEvent(event HookEvent) (db.AttentionState, bool) {
	switch event.HookEventName {
	case "SessionStart":
		return db.AttentionOK, true
	case "UserPromptSubmit":
		return db.AttentionOK, true
	case "Stop":
		return db.AttentionWaiting, true
	case "PermissionRequest":
		return db.AttentionPermission, true
	case "PostToolUse":
		return db.AttentionOK, true
	case "PostToolUseFailure":
		return db.AttentionOK, true
	case "StopFailure":
		return db.AttentionError, true
	case "TaskCompleted":
		return db.AttentionDone, true
	case "Notification":
		switch event.NotificationType {
		case "permission_prompt":
			return db.AttentionPermission, true
		case "idle_prompt":
			return db.AttentionWaiting, true
		}
		return "", false
	case "SessionEnd":
		return db.AttentionOK, true
	default:
		return "", false
	}
}
