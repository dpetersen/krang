package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/dpetersen/krang/internal/db"
)

type SparklineWindow int

const (
	SparklineWindow1m SparklineWindow = iota
	SparklineWindow10m
	SparklineWindow60m
)

const sparklineWidth = 20

func (w SparklineWindow) Duration() time.Duration {
	switch w {
	case SparklineWindow10m:
		return 10 * time.Minute
	case SparklineWindow60m:
		return 60 * time.Minute
	default:
		return 1 * time.Minute
	}
}

func (w SparklineWindow) Label() string {
	switch w {
	case SparklineWindow10m:
		return "10m"
	case SparklineWindow60m:
		return "60m"
	default:
		return "1m"
	}
}

func (w SparklineWindow) Next() SparklineWindow {
	switch w {
	case SparklineWindow1m:
		return SparklineWindow10m
	case SparklineWindow10m:
		return SparklineWindow60m
	default:
		return SparklineWindow1m
	}
}

// Activity phases ordered by display priority (highest first).
// Higher priority phases appear as the background (base) color
// when stacked with a lower priority phase.
type activityPhase int

const (
	phaseIdle       activityPhase = iota
	phaseWorking                        // UserPromptSubmit, SessionStart
	phaseToolCalls                      // PostToolUse
	phaseDone                           // Stop (done), TaskCompleted
	phaseWaiting                        // Stop (waiting)
	phaseError                          // StopFailure
	phasePermission                     // PermissionRequest
)

type sparklineBucket struct {
	counts     [7]int        // indexed by activityPhase
	stickyPhase activityPhase // fill-forward state for empty buckets
}

func (b *sparklineBucket) totalEvents() int {
	total := 0
	for _, c := range b.counts {
		total += c
	}
	return total
}

// topTwo returns the two highest-priority phases with non-zero counts.
// Returns primary (highest priority), secondary, and whether a secondary exists.
func (b *sparklineBucket) topTwo() (activityPhase, activityPhase, bool) {
	var primary, secondary activityPhase
	foundPrimary := false
	foundSecondary := false

	// Walk from highest priority down.
	for p := phasePermission; p >= phaseWorking; p-- {
		if b.counts[p] > 0 {
			if !foundPrimary {
				primary = p
				foundPrimary = true
			} else if !foundSecondary {
				secondary = p
				foundSecondary = true
				break
			}
		}
	}

	if !foundPrimary {
		return phaseIdle, phaseIdle, false
	}
	return primary, secondary, foundSecondary
}

func eventToPhase(eventType string) activityPhase {
	switch eventType {
	case "PostToolUse":
		return phaseToolCalls
	case "UserPromptSubmit", "SessionStart":
		return phaseWorking
	case "PermissionRequest":
		return phasePermission
	case "StopFailure":
		return phaseError
	case "TaskCompleted", "ClassifyDone":
		return phaseDone
	case "ClassifyWaiting":
		return phaseWaiting
	default:
		// Raw "Stop" events are not colored — the ClassifyDone/
		// ClassifyWaiting synthetic event that follows carries the
		// actual classification result.
		return phaseIdle
	}
}

// eventToStickyPhase returns the sticky state for state-transition events.
// Returns the phase and true if this event changes the sticky state.
func eventToStickyPhase(eventType string) (activityPhase, bool) {
	switch eventType {
	case "PermissionRequest":
		return phasePermission, true
	case "StopFailure":
		return phaseError, true
	case "ClassifyWaiting":
		return phaseWaiting, true
	case "ClassifyDone":
		return phaseDone, true
	case "Stop":
		// Raw Stop is not sticky — wait for ClassifyDone/ClassifyWaiting.
		return phaseIdle, false
	case "UserPromptSubmit", "SessionStart", "PostToolUse":
		// Activity resumes — clears any blocking sticky state.
		return phaseWorking, true
	case "TaskCompleted":
		return phaseDone, true
	default:
		return phaseIdle, false
	}
}

func bucketEvents(events []db.ActivityEvent, window time.Duration, bucketCount int, now time.Time) []sparklineBucket {
	buckets := make([]sparklineBucket, bucketCount)
	windowStart := now.Add(-window)
	bucketDuration := window / time.Duration(bucketCount)

	// Track sticky state as we iterate chronologically.
	currentSticky := phaseIdle

	// First pass: assign events to buckets and track sticky state per bucket.
	eventIdx := 0
	for i := range buckets {
		bucketStart := windowStart.Add(time.Duration(i) * bucketDuration)
		bucketEnd := bucketStart.Add(bucketDuration)

		buckets[i].stickyPhase = currentSticky

		for eventIdx < len(events) && events[eventIdx].CreatedAt.Before(bucketEnd) {
			if !events[eventIdx].CreatedAt.Before(bucketStart) {
				phase := eventToPhase(events[eventIdx].EventType)
				if phase != phaseIdle {
					buckets[i].counts[phase]++
				}
				if sticky, ok := eventToStickyPhase(events[eventIdx].EventType); ok {
					currentSticky = sticky
				}
			}
			eventIdx++
		}

		// Update sticky for next bucket based on events in this one.
		buckets[i].stickyPhase = currentSticky
	}

	return buckets
}

var blockChars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

func phaseColor(p activityPhase, theme Theme) lipgloss.Color {
	switch p {
	case phaseToolCalls:
		return theme.Accent
	case phaseWorking:
		return theme.Active
	case phaseWaiting:
		return theme.Warning
	case phaseDone:
		return theme.Done
	case phasePermission:
		return theme.Danger
	case phaseError:
		return theme.Error
	default:
		return theme.Dormant
	}
}

func renderSparkline(buckets []sparklineBucket, theme Theme, rowBg *lipgloss.Color) string {
	if len(buckets) == 0 {
		style := lipgloss.NewStyle().Foreground(theme.Dormant)
		if rowBg != nil {
			style = style.Background(*rowBg)
		}
		return strings.Repeat(style.Render("▁"), sparklineWidth)
	}

	// Find max event count for normalization.
	maxCount := 0
	for i := range buckets {
		total := buckets[i].totalEvents()
		if total > maxCount {
			maxCount = total
		}
	}

	var b strings.Builder
	for i := range buckets {
		total := buckets[i].totalEvents()

		if total == 0 {
			// No events — use sticky state at minimum height.
			sticky := buckets[i].stickyPhase
			var style lipgloss.Style
			if sticky == phaseIdle || sticky == phaseWorking {
				style = lipgloss.NewStyle().Foreground(theme.Dormant)
			} else {
				style = lipgloss.NewStyle().Foreground(phaseColor(sticky, theme))
			}
			if rowBg != nil {
				style = style.Background(*rowBg)
			}
			b.WriteString(style.Render("▁"))
			continue
		}

		// Determine height (1-8 scale).
		height := 1
		if maxCount > 0 {
			height = (total * 7 / maxCount) + 1
			if height > 8 {
				height = 8
			}
		}
		char := blockChars[height-1]

		primary, secondary, hasSecondary := buckets[i].topTwo()

		style := lipgloss.NewStyle().Foreground(phaseColor(primary, theme))
		if hasSecondary {
			// Stacked: background = secondary (lower priority),
			// foreground = primary (higher priority).
			style = style.Background(phaseColor(secondary, theme))
		} else if rowBg != nil {
			style = style.Background(*rowBg)
		}

		b.WriteString(style.Render(string(char)))
	}

	return b.String()
}
