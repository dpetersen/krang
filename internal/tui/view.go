package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/tmux"
)

func (m Model) View() string {
	var b strings.Builder

	b.WriteString(m.renderHeader())
	b.WriteString("\n\n")
	b.WriteString(m.renderTable())
	b.WriteString("\n")

	switch m.mode {
	case ModeNewName:
		b.WriteString("\n")
		b.WriteString(inputLabelStyle.Render("Task name: "))
		b.WriteString(m.nameInput.View())
	case ModeNewPrompt:
		b.WriteString("\n")
		b.WriteString(inputLabelStyle.Render(fmt.Sprintf("Prompt for %s: ", m.pendingNewName)))
		b.WriteString(m.promptInput.View())
	case ModeConfirmKill:
		t := m.selectedTask()
		if t != nil {
			b.WriteString("\n")
			b.WriteString(errorStyle.Render(fmt.Sprintf("Kill task %q? [y/N]", t.Name)))
		}
	case ModeFilter:
		b.WriteString("\n")
		b.WriteString(inputLabelStyle.Render("Filter: "))
		b.WriteString(m.filterInput.View())
	case ModeImportName:
		b.WriteString("\n")
		b.WriteString(inputLabelStyle.Render("Import name: "))
		b.WriteString(m.nameInput.View())
	case ModeImportSessionID:
		b.WriteString("\n")
		b.WriteString(inputLabelStyle.Render(fmt.Sprintf("Session ID for %s: ", m.pendingImportName)))
		b.WriteString(m.sessionIDInput.View())
	case ModeHelp:
		b.WriteString("\n")
		b.WriteString(m.renderHelp())
	default:
		if m.filterText != "" {
			b.WriteString(headerStyle.Render(fmt.Sprintf("  filter: %s (/ to change, esc to clear)", m.filterText)))
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

	krangCwd := tildeify(krangWorkingDir())
	cwdStr := headerStyle.Render(krangCwd)

	left := fmt.Sprintf(" %s  %s", title, stats)
	right := headerStyle.Render(clock)

	totalUsed := lipgloss.Width(left) + lipgloss.Width(cwdStr) + lipgloss.Width(right)
	leftGap := (m.width - totalUsed) / 2
	if leftGap < 1 {
		leftGap = 1
	}
	rightGap := m.width - lipgloss.Width(left) - leftGap - lipgloss.Width(cwdStr) - lipgloss.Width(right)
	if rightGap < 1 {
		rightGap = 1
	}

	return left + strings.Repeat(" ", leftGap) + cwdStr + strings.Repeat(" ", rightGap) + right
}

func (m Model) renderTable() string {
	tasks := m.filteredTasks()
	if len(tasks) == 0 {
		if m.filterText != "" {
			return headerStyle.Render("  No tasks match filter.")
		}
		return headerStyle.Render("  No tasks. Press [n] to create one.")
	}

	nameW := 20
	cwdW := 3
	for _, t := range tasks {
		if len(t.Name) > nameW {
			nameW = len(t.Name)
		}
		rc := relativeCwd(t.Cwd)
		if len(rc) > cwdW {
			cwdW = len(rc)
		}
	}
	if nameW > 30 {
		nameW = 30
	}
	if cwdW > 25 {
		cwdW = 25
	}

	header := fmt.Sprintf(
		"  %-4s %-*s %-8s %-6s %-*s %s",
		"#", nameW, "Name", "State", "Attn", cwdW, "Cwd", "Summary",
	)

	var b strings.Builder
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(headerStyle.Render("  " + strings.Repeat("-", m.width-4)))
	b.WriteString("\n")

	companionCounts := m.countCompanions(tasks)
	for i, t := range tasks {
		line := m.renderRow(i, t, nameW, cwdW, companionCounts[t.Name])
		b.WriteString(line)
		if i < len(tasks)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (m Model) countCompanions(tasks []db.Task) map[string]int {
	counts := make(map[string]int)
	for _, t := range tasks {
		if t.TmuxWindow == "" {
			continue
		}
		session := m.activeSession
		if t.State == db.StateParked {
			session = tmux.ParkedSession
		}
		counts[t.Name] = len(tmux.FindCompanions(session, t.Name))
	}
	return counts
}

func (m Model) renderRow(index int, t db.Task, nameW, cwdW, companionCount int) string {
	cursor := "  "
	if index == m.cursor {
		cursor = "> "
	}

	name := t.Name
	if companionCount > 0 {
		name += strings.Repeat("+", companionCount)
	}
	if len(name) > nameW {
		name = name[:nameW-1] + "~"
	}

	cwd := relativeCwd(t.Cwd)
	if len(cwd) > cwdW {
		cwd = cwd[:cwdW-1] + "~"
	}

	stateStr := renderState(t.State)
	attnStr := renderAttention(t.Attention)

	summary := t.Summary
	maxSummaryW := m.width - 4 - nameW - 8 - 6 - cwdW - 10
	if maxSummaryW < 10 {
		maxSummaryW = 10
	}
	if len(summary) > maxSummaryW {
		summary = summary[:maxSummaryW-1] + "~"
	}

	row := fmt.Sprintf(
		"%s%-4d %-*s %-8s %-6s %-*s %s",
		cursor, index+1, nameW, name, stateStr, attnStr, cwdW, cwd, summary,
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

	hints = append(hints, "[i]mport", "[r]efresh", "[/]filter", "[?]help", "[q]uit")

	return statusBarStyle.Render(strings.Join(hints, "  "))
}

func krangWorkingDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "?"
	}
	return cwd
}

func tildeify(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

func relativeCwd(taskCwd string) string {
	krangCwd := krangWorkingDir()
	if krangCwd != "" && strings.HasPrefix(taskCwd, krangCwd+"/") {
		rel := taskCwd[len(krangCwd)+1:]
		return rel
	}
	if taskCwd == krangCwd {
		return "."
	}
	return tildeify(taskCwd)
}

func (m Model) renderHelp() string {
	help := `
  Keybindings:

  n         Create new task
  Enter     Focus active task window
  p         Park task (move to background)
  u         Unpark task (bring back)
  d         Dormify task (save & close)
  w         Wake dormant task (resume)
  x         Kill task (with confirmation)
  c         Mark task completed
  r         Refresh AI summaries
  /         Filter tasks
  ?         Toggle this help
  j/k       Navigate up/down
  q         Quit krang (tasks keep running)

  Task States:
  active    Running in current tmux session
  parked    Running in background session
  dormant   Saved, not running (can wake)

  Attention:
  ok        Claude is working
  wait      Claude finished, needs your input
  PERM      Permission prompt blocking
  ERR       Something went wrong
  done      Task self-reported complete

  Press any key to close`

	return headerStyle.Render(help)
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
