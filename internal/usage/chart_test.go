package usage

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderChartBasic(t *testing.T) {
	now := time.Now()
	summary := &UsageSummary{
		Snapshots: []TokenSnapshot{
			{Timestamp: now, Model: "claude-opus-4-6", Input: 1000, Output: 100},
			{Timestamp: now.Add(time.Minute), Model: "claude-opus-4-6", Input: 2000, Output: 200},
			{Timestamp: now.Add(2 * time.Minute), Model: "claude-opus-4-6", Input: 3000, Output: 300},
		},
	}

	accent := lipgloss.Color("212")
	muted := lipgloss.Color("240")

	result := RenderChart(summary, 40, accent, muted)
	if result == "" {
		t.Fatal("expected non-empty chart")
	}

	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	if len(lines) != chartHeight {
		t.Errorf("expected %d rows, got %d", chartHeight, len(lines))
	}
}

func TestRenderChartEmptySnapshots(t *testing.T) {
	summary := &UsageSummary{
		Snapshots: nil,
	}
	result := RenderChart(summary, 40, lipgloss.Color("212"), lipgloss.Color("240"))
	if result != "" {
		t.Error("expected empty string for no snapshots")
	}
}

func TestRenderChartTooNarrow(t *testing.T) {
	summary := &UsageSummary{
		Snapshots: []TokenSnapshot{
			{Timestamp: time.Now(), Model: "claude-opus-4-6", Input: 1000, Output: 100},
		},
	}
	result := RenderChart(summary, 5, lipgloss.Color("212"), lipgloss.Color("240"))
	if result != "" {
		t.Error("expected empty string for narrow width")
	}
}

func TestCumulativeTokenBuckets(t *testing.T) {
	now := time.Now()
	summary := &UsageSummary{
		Snapshots: []TokenSnapshot{
			{Timestamp: now, Model: "claude-opus-4-6", Input: 100, Output: 50, CacheRead: 1000},
			{Timestamp: now.Add(time.Minute), Model: "claude-opus-4-6", Input: 200, Output: 100, CacheRead: 2000},
		},
	}

	buckets := cumulativeTokenBuckets(summary, 10)
	if len(buckets) != 10 {
		t.Fatalf("expected 10 buckets, got %d", len(buckets))
	}

	for i := 1; i < len(buckets); i++ {
		if buckets[i] < buckets[i-1] {
			t.Errorf("bucket %d (%d) < bucket %d (%d)", i, buckets[i], i-1, buckets[i-1])
		}
	}

	// Last bucket should have total tokens.
	expectedTotal := (100 + 50 + 1000) + (200 + 100 + 2000)
	if buckets[len(buckets)-1] != expectedTotal {
		t.Errorf("last bucket = %d, want %d", buckets[len(buckets)-1], expectedTotal)
	}
}

func TestFormatCost(t *testing.T) {
	tests := []struct {
		input    float64
		expected string
	}{
		{0.001, "$0.0010"},
		{0.05, "$0.05"},
		{1.234, "$1.23"},
	}
	for _, tt := range tests {
		got := FormatCost(tt.input)
		if got != tt.expected {
			t.Errorf("FormatCost(%f) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
