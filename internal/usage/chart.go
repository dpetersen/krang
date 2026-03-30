package usage

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const chartHeight = 8

// RenderChart draws a cumulative token usage bar chart. Each column is a
// time bucket; height represents cumulative total tokens at that point.
func RenderChart(summary *UsageSummary, width int, accent lipgloss.Color, muted lipgloss.Color) string {
	if len(summary.Snapshots) == 0 || width < 10 {
		return ""
	}

	cumulative := cumulativeTokenBuckets(summary, 0)
	maxTokens := cumulative[len(cumulative)-1]
	if maxTokens == 0 {
		return ""
	}

	maxLabel := FormatTokenCount(maxTokens)
	labelWidth := len(maxLabel)
	if labelWidth < 2 {
		labelWidth = 2
	}

	// 2 extra chars for "┤" gutter + space.
	chartWidth := width - labelWidth - 2
	if chartWidth < 5 {
		chartWidth = 5
	}

	// Re-bucket with actual chart width.
	cumulative = cumulativeTokenBuckets(summary, chartWidth)
	maxTokens = cumulative[len(cumulative)-1]
	maxLabel = FormatTokenCount(maxTokens)

	accentStyle := lipgloss.NewStyle().Foreground(accent)
	mutedStyle := lipgloss.NewStyle().Foreground(muted)

	var b strings.Builder

	for row := range chartHeight {
		threshold := float64(chartHeight-row) / float64(chartHeight)

		switch row {
		case 0:
			b.WriteString(mutedStyle.Render(fmt.Sprintf("%*s", labelWidth, maxLabel)))
		case chartHeight - 1:
			b.WriteString(mutedStyle.Render(fmt.Sprintf("%*s", labelWidth, "0")))
		default:
			b.WriteString(strings.Repeat(" ", labelWidth))
		}

		if row == 0 || row == chartHeight-1 {
			b.WriteString(mutedStyle.Render("┤"))
		} else {
			b.WriteString(mutedStyle.Render("│"))
		}

		for _, tokens := range cumulative {
			normalized := float64(tokens) / float64(maxTokens)
			if normalized >= threshold {
				b.WriteString(accentStyle.Render("█"))
			} else {
				b.WriteString(" ")
			}
		}

		b.WriteString("\n")
	}

	return b.String()
}

// cumulativeTokenBuckets divides snapshots into `n` time buckets and
// returns the cumulative total token count at each bucket boundary.
// If n <= 0, uses len(summary.Snapshots) as the bucket count.
func cumulativeTokenBuckets(summary *UsageSummary, n int) []int {
	snaps := summary.Snapshots
	if len(snaps) == 0 {
		return nil
	}

	if n <= 0 {
		n = len(snaps)
	}

	start := snaps[0].Timestamp
	end := snaps[len(snaps)-1].Timestamp
	duration := end.Sub(start)
	if duration <= 0 {
		total := 0
		for _, s := range snaps {
			total += s.Input + s.Output + s.CacheCreate + s.CacheRead
		}
		return []int{total}
	}

	buckets := make([]int, n)
	running := 0
	snapIdx := 0

	for i := range n {
		bucketEnd := start.Add(duration * time.Duration(i+1) / time.Duration(n))
		for snapIdx < len(snaps) && !snaps[snapIdx].Timestamp.After(bucketEnd) {
			s := snaps[snapIdx]
			running += s.Input + s.Output + s.CacheCreate + s.CacheRead
			snapIdx++
		}
		buckets[i] = running
	}

	return buckets
}

// FormatCost formats a dollar amount for display.
func FormatCost(cost float64) string {
	if cost < 0.01 {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
}

// FormatTokenCount returns a human-friendly abbreviated token count
// (e.g. "1.2M", "48K", "350").
func FormatTokenCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
