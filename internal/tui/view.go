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
		b.WriteString("\n")
		b.WriteString(headerStyle.Render("  Enter to create, Ctrl+F for flags"))
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
	case ModeFlagEdit, ModeNewFlags:
		b.WriteString("\n")
		b.WriteString(m.renderFlagEdit())
	case ModeConfirmRelaunch:
		b.WriteString("\n")
		b.WriteString(errorStyle.Render("Flags changed. Claude will be relaunched (session resumes). Proceed? [y/N]"))
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

	if m.mode == ModeSitRepLoading {
		return titleStyle.Render(" KRANG") + "  " + headerStyle.Render("Generating sit rep...") + "\n"
	}
	if m.mode == ModeSitRep {
		header := titleStyle.Render(" SIT REP") + "  " + headerStyle.Render("(q/esc to close, j/k to scroll)")
		return header + "\n\n" + m.sitRepViewport.View() + "\n"
	}

	return b.String()
}

func wordWrap(text string, width int) string {
	var result strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if len(line) <= width {
			result.WriteString(line)
			result.WriteByte('\n')
			continue
		}
		remaining := line
		for len(remaining) > width {
			breakAt := width
			// Try to break at a space.
			for i := width; i > width/2; i-- {
				if remaining[i] == ' ' {
					breakAt = i
					break
				}
			}
			result.WriteString(remaining[:breakAt])
			result.WriteByte('\n')
			remaining = strings.TrimLeft(remaining[breakAt:], " ")
		}
		if remaining != "" {
			result.WriteString(remaining)
			result.WriteByte('\n')
		}
	}
	return strings.TrimRight(result.String(), "\n")
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
	sortIndicator := ""
	if m.sortByPriority {
		sortIndicator = " | Priority"
	}
	stats := headerStyle.Render(fmt.Sprintf(
		"Active: %d | Parked: %d | Frozen: %d%s",
		activeCt, parkedCt, dormantCt, sortIndicator,
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
			session = m.parkedSession
		}
		counts[t.Name] = len(tmux.FindCompanions(session, t.Name))
	}
	return counts
}

func padRight(s string, width int) string {
	visible := lipgloss.Width(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
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
	if t.Flags.HasNonDefault() {
		name = flagSkullStyle.Render("☠") + " " + name
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

	row := cursor + padRight(fmt.Sprintf("%-4d", index+1), 4) + " " +
		padRight(name, nameW) + " " +
		padRight(stateStr, 8) + " " +
		padRight(attnStr, 6) + " " +
		padRight(cwd, cwdW) + " " +
		summary

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
		return stateDormantStyle.Render("frozen")
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
			hints = append(hints, "[enter]focus", "[+]companion", "[p]ark", "[f]reeze")
		case db.StateParked:
			hints = append(hints, "[u]npark", "[f]reeze")
		case db.StateDormant:
			hints = append(hints, "[t]haw")
		}
		if t.State != db.StateCompleted && t.State != db.StateFailed {
			hints = append(hints, "[F]lags")
		}
		hints = append(hints, "[x]kill", "[c]omplete")
	}

	hints = append(hints, "[i]mport")
	if len(m.tasks) > 0 {
		hints = append(hints, "[S]itrep", "re[s]ort", "[/]filter")
	}
	hints = append(hints, "[C]ompact", "[?]help", "[q]uit")

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
  i         Import existing Claude session
  Enter     Focus active task window
  p         Park task (move to background)
  u         Unpark task (bring back)
  f         Freeze task (save & close)
  t         Thaw frozen task (resume)
  x         Kill task (with confirmation)
  c         Mark task completed
  +         Create companion window for task
  F         Edit task flags (sandbox, permissions)
  s         Toggle sort (created / priority)
  S         Sit rep (briefing on all active tasks)
  r         Refresh AI summaries
  C         Compact windows (renumber sequentially)
  /         Filter tasks (esc to clear)
  ?         Toggle this help
  j/k       Navigate up/down
  q         Quit krang (tasks keep running)

  Task States:
  active    Running in current tmux session
  parked    Running in background session
  frozen    Saved, not running (can thaw)

  Attention:
  ok        Claude is working
  wait      Claude finished, needs your input
  PERM      Permission prompt blocking
  ERR       Something went wrong
  done      Task self-reported complete

  Press any key to close`

	return headerStyle.Render(help)
}

func (m Model) renderFlagEdit() string {
	var b strings.Builder

	title := "Task Flags"
	if m.mode == ModeFlagEdit {
		for _, t := range m.tasks {
			if t.ID == m.flagEditTaskID {
				title += ": " + t.Name
				break
			}
		}
	} else {
		title += ": " + m.pendingNewName
	}
	b.WriteString(inputLabelStyle.Render("  " + title))
	b.WriteString("\n\n")

	for i, fd := range flagDefinitions {
		cursor := "  "
		if i == m.flagEditCursor {
			cursor = "> "
		}
		check := "[ ]"
		if fd.Get(m.pendingFlags) {
			check = "[x]"
		}
		b.WriteString(fmt.Sprintf("  %s%s %s — %s\n", cursor, check, fd.Label, fd.Description))
	}

	b.WriteString("\n")
	b.WriteString(headerStyle.Render("  j/k: navigate  space: toggle  enter: apply  esc: cancel"))
	return b.String()
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
