package hooks

import (
	"testing"

	"github.com/dpetersen/krang/internal/db"
)

func TestAttentionFromEvent(t *testing.T) {
	tests := []struct {
		event         HookEvent
		wantAttention db.AttentionState
		wantOK        bool
	}{
		{
			event:         HookEvent{HookEventName: "SessionStart"},
			wantAttention: db.AttentionOK,
			wantOK:        true,
		},
		{
			event:         HookEvent{HookEventName: "Stop"},
			wantAttention: db.AttentionWaiting,
			wantOK:        true,
		},
		{
			event:         HookEvent{HookEventName: "PermissionRequest"},
			wantAttention: db.AttentionPermission,
			wantOK:        true,
		},
		{
			event:         HookEvent{HookEventName: "PostToolUse"},
			wantAttention: db.AttentionOK,
			wantOK:        true,
		},
		{
			event:         HookEvent{HookEventName: "StopFailure"},
			wantAttention: db.AttentionError,
			wantOK:        true,
		},
		{
			event:         HookEvent{HookEventName: "TaskCompleted"},
			wantAttention: db.AttentionDone,
			wantOK:        true,
		},
		{
			event:         HookEvent{HookEventName: "Notification", NotificationType: "permission_prompt"},
			wantAttention: db.AttentionPermission,
			wantOK:        true,
		},
		{
			event:         HookEvent{HookEventName: "Notification", NotificationType: "idle_prompt"},
			wantAttention: db.AttentionWaiting,
			wantOK:        true,
		},
		{
			event:  HookEvent{HookEventName: "Notification", NotificationType: "auth_success"},
			wantOK: false,
		},
		{
			event:         HookEvent{HookEventName: "SessionEnd"},
			wantAttention: db.AttentionOK,
			wantOK:        true,
		},
		{
			event:  HookEvent{HookEventName: "UnknownEvent"},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		name := tt.event.HookEventName
		if tt.event.NotificationType != "" {
			name += "/" + tt.event.NotificationType
		}
		t.Run(name, func(t *testing.T) {
			got, ok := AttentionFromEvent(tt.event)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.wantAttention {
				t.Errorf("attention = %v, want %v", got, tt.wantAttention)
			}
		})
	}
}
