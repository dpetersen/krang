package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	ltable "github.com/charmbracelet/lipgloss/table"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/tmux"
)

func (m Model) View() string {
	if m.mode == ModeSitRepLoading {
		return m.styles.Title.Render(" KRANG") + "  " + m.styles.Header.Render("Generating sit rep...") + "\n"
	}
	if m.mode == ModeSitRep {
		header := m.styles.Title.Render(" SIT REP") + "  " + m.styles.Header.Render("(q/esc to close, j/k to scroll)")
		return header + "\n\n" + m.sitRepViewport.View() + "\n"
	}
	if m.mode == ModeHelp {
		return m.renderHelp()
	}

	// Build top section: header + table + mode-specific UI.
	var top strings.Builder
	top.WriteString(m.renderHeader())
	top.WriteString("\n\n")
	top.WriteString(m.renderTable())
	top.WriteString("\n")

	switch m.mode {
	case ModeConfirmKill:
		t := m.selectedTask()
		if t != nil {
			top.WriteString("\n")
			top.WriteString(m.styles.ErrorText.Render(fmt.Sprintf("Kill task %q? [y/N]", t.Name)))
		}
	case ModeFilter:
		top.WriteString("\n")
		top.WriteString(m.styles.InputLabel.Render("Filter: "))
		top.WriteString(m.filterInput.View())
	case ModeForm:
		if m.activeForm != nil {
			top.WriteString("\n")
			top.WriteString(m.activeForm.View())
		}
	case ModeConfirmRelaunch:
		top.WriteString("\n")
		top.WriteString(m.styles.ErrorText.Render("Flags changed. Claude will be relaunched (session resumes). Proceed? [y/N]"))
	default:
		if m.filterText != "" {
			top.WriteString(m.styles.Header.Render(fmt.Sprintf("  filter: %s (/ to change, esc to clear)", m.filterText)))
		}
		top.WriteString("\n")
		top.WriteString(m.renderStatusBar())
	}

	// Build bottom section: debug log (fixed height).
	bottom := m.renderDebugLog()

	// Pad between top and bottom to pin the log to the screen bottom.
	topStr := top.String()
	topLines := strings.Count(topStr, "\n") + 1
	bottomLines := maxVisibleLogLines + 3 // log lines + manual top border + left/right/bottom border (2 lines)
	gap := m.height - topLines - bottomLines
	if gap < 0 {
		gap = 0
	}

	return topStr + strings.Repeat("\n", gap) + bottom
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
	title := m.styles.Title.Render("KRANG")
	sortIndicator := ""
	if m.sortByPriority {
		sortIndicator = " | Priority"
	}
	stats := m.styles.Header.Render(fmt.Sprintf(
		"Active: %d | Parked: %d | Frozen: %d%s",
		activeCt, parkedCt, dormantCt, sortIndicator,
	))

	krangCwd := tildeify(krangWorkingDir())
	cwdStr := m.styles.Header.Render(krangCwd)

	left := fmt.Sprintf(" %s  %s", title, stats)
	right := m.styles.Header.Render(clock)

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
			return m.styles.Header.Render("  No tasks match filter.")
		}
		return m.styles.Header.Render("  No tasks. Press [n] to create one.")
	}

	companionCounts := m.countCompanions(tasks)
	rows := make([][]string, len(tasks))
	for i, t := range tasks {
		name := t.Name
		if companionCounts[t.Name] > 0 {
			name += strings.Repeat("+", companionCounts[t.Name])
		}
		if t.Flags.HasNonDefault() {
			name = "☠ " + name
		}

		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}

		rows[i] = []string{
			cursor,
			fmt.Sprintf("%d", i+1),
			name,
			stateLabel(t.State),
			attentionLabel(t.Attention),
			relativeCwd(t.Cwd),
			t.Summary,
		}
	}

	t := ltable.New().
		Headers("", "#", "Name", "State", "Attn", "Cwd", "Summary").
		Rows(rows...).
		Border(lipgloss.HiddenBorder()).
		BorderColumn(false).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == ltable.HeaderRow {
				return m.styles.Header.PaddingRight(1)
			}

			base := lipgloss.NewStyle().PaddingRight(1)
			if row < 0 || row >= len(tasks) {
				return base
			}

			style := m.taskRowStyle(tasks[row])
			style = style.PaddingRight(1)
			if row == m.cursor {
				style = style.Background(m.styles.SelectedRow.GetBackground()).Bold(true)
			}
			return style
		})

	return t.Render()
}

func (m Model) taskRowStyle(t db.Task) lipgloss.Style {
	if t.State == db.StateDormant {
		return m.styles.StateDormant
	}
	if t.State == db.StateParked {
		return m.styles.StateParked
	}

	switch t.Attention {
	case db.AttentionPermission:
		return m.styles.AttentionPermission
	case db.AttentionError:
		return m.styles.AttentionError
	case db.AttentionWaiting:
		return m.styles.AttentionWaiting
	case db.AttentionDone:
		return m.styles.AttentionDone
	default:
		return lipgloss.NewStyle()
	}
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

func stateLabel(state db.TaskState) string {
	switch state {
	case db.StateActive:
		return "active"
	case db.StateParked:
		return "parked"
	case db.StateDormant:
		return "frozen"
	default:
		return string(state)
	}
}

func attentionLabel(attention db.AttentionState) string {
	switch attention {
	case db.AttentionOK:
		return "ok"
	case db.AttentionWaiting:
		return "wait"
	case db.AttentionPermission:
		return "PERM"
	case db.AttentionError:
		return "ERR"
	case db.AttentionDone:
		return "done"
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

	left := strings.Join(hints, "  ")

	taskCount := len(m.filteredTasks())
	totalCount := len(m.tasks)
	var right string
	if m.filterText != "" {
		right = fmt.Sprintf("%d/%d tasks", taskCount, totalCount)
	} else {
		right = fmt.Sprintf("%d tasks", totalCount)
	}
	if m.sortByPriority {
		right += " | priority"
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		gap = 2
	}

	bar := left + strings.Repeat(" ", gap) + right
	return m.styles.StatusBar.Render(bar)
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
	header := m.styles.Title.Render(" HELP") + "  " + m.styles.Header.Render("(q/esc/? to close, j/k to scroll)")
	return header + "\n\n" + m.helpViewport.View() + "\n"
}

func buildHelpContent() string {
	return `Keybindings

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

Task States

  active    Running in krang's tmux session. Claude Code is
            actively working or waiting for input.
  parked    Moved to a background tmux session. Claude is
            still running but not visible. Park tasks to
            reduce clutter.
  frozen    No tmux window. Session ID saved so Claude can
            resume with --resume. Use this for tasks you want
            to pause without killing.

Attention States

  ok        Claude is working normally.
  wait      Claude stopped and is waiting for your input.
            Switch to the task window to continue.
  PERM      A permission prompt is blocking Claude. Approve
            or deny it in the task window.
  ERR       Something went wrong (e.g. stop failure).
  done      Claude self-reported the task as complete via
            the TaskCompleted hook.

Glossary

  Companion window
    A shell window (KF!<name>) associated with a task. Created
    with +, it travels with the task on park/unpark and is
    killed on freeze. Useful for running tests, watching logs,
    or other activities alongside Claude.

  Window naming
    K!<name>   Task window (managed by krang)
    KF!<name>  Companion window (follows the task)
    Windows without these prefixes are never touched.

  Sort modes
    Created (default) shows all tasks in creation order.
    Priority shows only active tasks sorted by attention
    urgency: PERM > ERR > wait > ok > done.`
}


const maxVisibleLogLines = 10

func (m Model) renderDebugLog() string {
	lines := m.debugLog
	if len(lines) > maxVisibleLogLines {
		lines = lines[len(lines)-maxVisibleLogLines:]
	}

	var content strings.Builder
	for i := 0; i < maxVisibleLogLines; i++ {
		if i < len(lines) {
			content.WriteString(m.styles.DebugLog.Render(lines[i]))
		}
		if i < maxVisibleLogLines-1 {
			content.WriteString("\n")
		}
	}

	borderColor := m.styles.Header.GetForeground()
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	innerWidth := m.width - 4 // account for left/right border + padding

	// Build top border with embedded label.
	label := " Events "
	lineLen := innerWidth - lipgloss.Width(label)
	if lineLen < 0 {
		lineLen = 0
	}
	topBorder := borderStyle.Render("╭─"+label) + borderStyle.Render(strings.Repeat("─", lineLen)+"╮")

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		BorderTop(false).
		Padding(0, 1).
		Width(m.width - 2)

	return topBorder + "\n" + boxStyle.Render(content.String())
}
