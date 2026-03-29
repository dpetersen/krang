package tui

import (
	"testing"
	"time"

	"github.com/dpetersen/krang/internal/db"
)

func TestSparklineWindowCycle(t *testing.T) {
	w := SparklineWindow1m
	w = w.Next()
	if w != SparklineWindow10m {
		t.Errorf("expected 10m, got %s", w.Label())
	}
	w = w.Next()
	if w != SparklineWindow60m {
		t.Errorf("expected 60m, got %s", w.Label())
	}
	w = w.Next()
	if w != SparklineWindow1m {
		t.Errorf("expected 1m, got %s", w.Label())
	}
}

func TestSparklineWindowDurations(t *testing.T) {
	if SparklineWindow1m.Duration() != time.Minute {
		t.Error("1m duration wrong")
	}
	if SparklineWindow10m.Duration() != 10*time.Minute {
		t.Error("10m duration wrong")
	}
	if SparklineWindow60m.Duration() != 60*time.Minute {
		t.Error("60m duration wrong")
	}
}

func TestBucketEventsBasic(t *testing.T) {
	now := time.Now()
	events := []db.ActivityEvent{
		{EventType: "PostToolUse", CreatedAt: now.Add(-30 * time.Second)},
		{EventType: "PostToolUse", CreatedAt: now.Add(-25 * time.Second)},
		{EventType: "PostToolUse", CreatedAt: now.Add(-10 * time.Second)},
	}

	buckets := bucketEvents(events, time.Minute, 4, now)
	if len(buckets) != 4 {
		t.Fatalf("expected 4 buckets, got %d", len(buckets))
	}

	// Events at -30s and -25s should be in bucket 2 (15s-30s before now).
	// Event at -10s should be in bucket 3 (0s-15s before now).
	totalEvents := 0
	for _, b := range buckets {
		totalEvents += b.totalEvents()
	}
	if totalEvents != 3 {
		t.Errorf("expected 3 total events, got %d", totalEvents)
	}
}

func TestBucketEventsStickyState(t *testing.T) {
	now := time.Now()
	events := []db.ActivityEvent{
		{EventType: "PermissionRequest", CreatedAt: now.Add(-50 * time.Second)},
		// No more events — permission should be sticky.
	}

	buckets := bucketEvents(events, time.Minute, 4, now)

	// The permission event is in the first bucket. Subsequent buckets
	// should have stickyPhase = phasePermission.
	for i := 1; i < len(buckets); i++ {
		if buckets[i].stickyPhase != phasePermission {
			t.Errorf("bucket %d: expected sticky permission, got %d", i, buckets[i].stickyPhase)
		}
	}
}

func TestBucketEventsStickyCleared(t *testing.T) {
	now := time.Now()
	events := []db.ActivityEvent{
		{EventType: "PermissionRequest", CreatedAt: now.Add(-50 * time.Second)},
		{EventType: "UserPromptSubmit", CreatedAt: now.Add(-20 * time.Second)},
	}

	buckets := bucketEvents(events, time.Minute, 4, now)

	// After UserPromptSubmit, sticky should clear to phaseWorking.
	lastBucket := buckets[len(buckets)-1]
	if lastBucket.stickyPhase != phaseWorking {
		t.Errorf("last bucket: expected sticky working, got %d", lastBucket.stickyPhase)
	}
}

func TestBucketTopTwo(t *testing.T) {
	b := sparklineBucket{}
	b.counts[phaseToolCalls] = 5
	b.counts[phasePermission] = 1

	primary, secondary, hasSecondary := b.topTwo()
	if primary != phasePermission {
		t.Errorf("expected primary=permission, got %d", primary)
	}
	if !hasSecondary || secondary != phaseToolCalls {
		t.Errorf("expected secondary=toolCalls, got %d (has=%v)", secondary, hasSecondary)
	}
}

func TestBucketTopTwoSingle(t *testing.T) {
	b := sparklineBucket{}
	b.counts[phaseToolCalls] = 3

	primary, _, hasSecondary := b.topTwo()
	if primary != phaseToolCalls {
		t.Errorf("expected primary=toolCalls, got %d", primary)
	}
	if hasSecondary {
		t.Error("expected no secondary")
	}
}

func TestBucketTopTwoEmpty(t *testing.T) {
	b := sparklineBucket{}

	primary, _, hasSecondary := b.topTwo()
	if primary != phaseIdle {
		t.Errorf("expected primary=idle, got %d", primary)
	}
	if hasSecondary {
		t.Error("expected no secondary")
	}
}

func TestRenderSparklineEmpty(t *testing.T) {
	result := renderSparkline(nil, Theme{})
	if len(result) == 0 {
		t.Error("expected non-empty string for nil buckets")
	}
}

func TestRenderSparklineProducesOutput(t *testing.T) {
	buckets := make([]sparklineBucket, sparklineWidth)
	buckets[5].counts[phaseToolCalls] = 3
	buckets[10].counts[phasePermission] = 1
	buckets[10].counts[phaseToolCalls] = 2

	result := renderSparkline(buckets, Theme{})
	if len(result) == 0 {
		t.Error("expected non-empty sparkline")
	}
}

func TestEventToPhase(t *testing.T) {
	tests := []struct {
		event string
		want  activityPhase
	}{
		{"PostToolUse", phaseToolCalls},
		{"UserPromptSubmit", phaseWorking},
		{"SessionStart", phaseWorking},
		{"PermissionRequest", phasePermission},
		{"StopFailure", phaseError},
		{"TaskCompleted", phaseDone},
		{"Unknown", phaseIdle},
	}

	for _, tc := range tests {
		got := eventToPhase(tc.event)
		if got != tc.want {
			t.Errorf("eventToPhase(%q) = %d, want %d", tc.event, got, tc.want)
		}
	}
}
