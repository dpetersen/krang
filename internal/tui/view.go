package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/dpetersen/krang/internal/db"
)

func (m Model) View() string {
	var b strings.Builder

	b.WriteString(m.renderHeader())
	b.WriteString("\n\n")
	b.WriteString(m.renderTable())
	b.WriteString("\n")

	if m.mode == ModeNewName {
		b.WriteString("\n")
		b.WriteString(inputLabelStyle.Render("Task name: "))
		b.WriteString(m.nameInput.View())
	} else if m.mode == ModeNewPrompt {
		b.WriteString("\n")
		b.WriteString(inputLabelStyle.Render(fmt.Sprintf("Prompt for %s: ", m.pendingNewName)))
		b.WriteString(m.promptInput.View())
	} else if m.mode == ModeConfirmKill {
		t := m.selectedTask()
		if t != nil {
			b.WriteString("\n")
			b.WriteString(errorStyle.Render(fmt.Sprintf("Kill task %q? [y/N]", t.Name)))
		}
	} else {
		if m.lastError != "" && time.Now().Before(m.errorExpires) {
			b.WriteString("\n")
			b.WriteString(errorStyle.Render("Error: " + m.lastError))
		}
		b.WriteString("\n")
		b.WriteString(m.renderStatusBar())
	}

	debugSection := m.renderDebugLog()
	if debugSection != "" {
		b.WriteString("\n\n")
		b.WriteString(debugSection)
	}

	return b.String()
}

func (m Model) renderHeader() string {
	activeCt, parkedCt, dormantCt := 0, 0, 0
	for _, t := range m.tasks {
		switch t.State {
		case db.StateActive:
			activeCt++
		case db.StateParked:
			parkedCt++
		case db.StateDormant:
			dormantCt++
		}
	}

	clock := time.Now().Format("15:04:05")
	title := titleStyle.Render("KRANG")
	stats := headerStyle.Render(fmt.Sprintf(
		"Active: %d | Parked: %d | Dormant: %d",
		activeCt, parkedCt, dormantCt,
	))

	left := fmt.Sprintf(" %s  %s", title, stats)
	right := headerStyle.Render(clock)

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	return left + strings.Repeat(" ", gap) + right
}

func (m Model) renderTable() string {
	if len(m.tasks) == 0 {
		return headerStyle.Render("  No tasks. Press [n] to create one.")
	}

	nameW := 20
	for _, t := range m.tasks {
		if len(t.Name) > nameW {
			nameW = len(t.Name)
		}
	}
	if nameW > 30 {
		nameW = 30
	}

	header := fmt.Sprintf(
		"  %-4s %-*s %-8s %-6s %s",
		"#", nameW, "Name", "State", "Attn", "Summary",
	)

	var b strings.Builder
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(headerStyle.Render("  " + strings.Repeat("-", m.width-4)))
	b.WriteString("\n")

	for i, t := range m.tasks {
		line := m.renderRow(i, t, nameW)
		b.WriteString(line)
		if i < len(m.tasks)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (m Model) renderRow(index int, t db.Task, nameW int) string {
	cursor := "  "
	if index == m.cursor {
		cursor = "> "
	}

	name := t.Name
	if len(name) > nameW {
		name = name[:nameW-1] + "~"
	}

	stateStr := renderState(t.State)
	attnStr := renderAttention(t.Attention)

	summary := t.Summary
	maxSummaryW := m.width - 4 - nameW - 8 - 6 - 8
	if maxSummaryW < 10 {
		maxSummaryW = 10
	}
	if len(summary) > maxSummaryW {
		summary = summary[:maxSummaryW-1] + "~"
	}

	row := fmt.Sprintf(
		"%s%-4d %-*s %-8s %-6s %s",
		cursor, index+1, nameW, name, stateStr, attnStr, summary,
	)

	if index == m.cursor {
		return selectedStyle.Render(row)
	}
	return row
}

func renderState(state db.TaskState) string {
	switch state {
	case db.StateActive:
		return stateActiveStyle.Render("active")
	case db.StateParked:
		return stateParkedStyle.Render("parked")
	case db.StateDormant:
		return stateDormantStyle.Render("dormant")
	default:
		return string(state)
	}
}

func renderAttention(attention db.AttentionState) string {
	switch attention {
	case db.AttentionOK:
		return attentionOKStyle.Render("ok")
	case db.AttentionWaiting:
		return attentionWaitingStyle.Render("wait")
	case db.AttentionPermission:
		return attentionPermissionStyle.Render("PERM")
	case db.AttentionError:
		return attentionErrorStyle.Render("ERR")
	case db.AttentionDone:
		return attentionDoneStyle.Render("done")
	default:
		return string(attention)
	}
}

func (m Model) renderStatusBar() string {
	t := m.selectedTask()
	var hints []string

	hints = append(hints, "[n]ew")

	if t != nil {
		switch t.State {
		case db.StateActive:
			hints = append(hints, "[enter]focus", "[p]ark", "[d]ormify")
		case db.StateParked:
			hints = append(hints, "[u]npark", "[d]ormify")
		case db.StateDormant:
			hints = append(hints, "[w]ake")
		}
		hints = append(hints, "[x]kill", "[c]omplete")
	}

	hints = append(hints, "[q]uit")

	return statusBarStyle.Render(strings.Join(hints, "  "))
}

func (m Model) renderDebugLog() string {
	if len(m.debugLog) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(headerStyle.Render("  Events"))
	b.WriteString("\n")
	for _, line := range m.debugLog {
		b.WriteString(debugLogStyle.Render("  " + line))
		b.WriteString("\n")
	}
	return b.String()
}
