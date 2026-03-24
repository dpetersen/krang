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
	"github.com/dpetersen/krang/internal/workspace"
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

	var modalContent string

	switch m.mode {
	case ModeConfirmComplete:
		if t := m.selectedTask(); t != nil {
			modalContent = m.renderConfirmComplete(t)
		}
	case ModeDetail:
		if t := m.selectedTask(); t != nil {
			modalContent = m.renderDetailModal(t)
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
	case ModeRepoSelect:
		if m.activeRepoPicker != nil {
			top.WriteString("\n")
			top.WriteString(m.activeRepoPicker.view())
		}
	case ModeWorkspaceProgress:
		top.WriteString("\n")
		top.WriteString(m.renderWorkspaceProgress())
	case ModeConfirmRelaunch:
		top.WriteString("\n")
		top.WriteString(m.styles.ErrorText.Render("Flags changed. Claude will be relaunched (session resumes). Proceed? [y/N]"))
	}

	// Status bar shown in normal mode and modal overlay modes (where it's
	// visible behind the overlay).
	if m.mode == ModeNormal || m.mode == ModeDetail || m.mode == ModeConfirmComplete {
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

	background := topStr + strings.Repeat("\n", gap) + bottom

	if modalContent != "" {
		return overlayCenter(background, modalContent, m.width, m.height)
	}
	return background
}

// overlayCenter composites a foreground modal on top of a background view,
// centered both horizontally and vertically. Background lines behind the
// modal are dimmed with ANSI dim attribute.
func overlayCenter(bg, fg string, width, height int) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	// Pad background to full terminal height.
	for len(bgLines) < height {
		bgLines = append(bgLines, "")
	}

	fgHeight := len(fgLines)
	fgWidth := 0
	for _, line := range fgLines {
		if w := lipgloss.Width(line); w > fgWidth {
			fgWidth = w
		}
	}

	startRow := (height - fgHeight) / 2
	startCol := (width - fgWidth) / 2
	if startRow < 0 {
		startRow = 0
	}
	if startCol < 0 {
		startCol = 0
	}

	dim := lipgloss.NewStyle().Faint(true)
	pad := strings.Repeat(" ", startCol)

	result := make([]string, len(bgLines))
	for i, bgLine := range bgLines {
		fgIdx := i - startRow
		if fgIdx >= 0 && fgIdx < fgHeight {
			// This row has modal content — dimmed padding + foreground.
			result[i] = dim.Render(pad) + fgLines[fgIdx]
		} else {
			// Row outside modal — dim the entire line.
			result[i] = dim.Render(stripAnsi(bgLine))
		}
	}

	return strings.Join(result, "\n")
}

// stripAnsi removes ANSI escape sequences from a string so it can be
// re-styled (e.g. dimmed) without stacking escape codes.
func stripAnsi(s string) string {
	var result strings.Builder
	inEscape := false
	for _, r := range s {
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
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

		attn := m.attentionWithProcs(t)
		if op, ok := m.pendingOps[t.ID]; ok {
			attn = m.spinner.View() + " " + op
		}

		windowIdx := ""
		if t.State == db.StateActive && t.TmuxWindow != "" {
			windowIdx = m.windowIndexes[t.TmuxWindow]
		}

		rows[i] = []string{
			cursor,
			windowIdx,
			name,
			stateLabel(t.State),
			attn,
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

// attentionWithProcs returns the attention label, appending ⚙N when
// Claude is stopped but background child processes are still running.
func (m Model) attentionWithProcs(t db.Task) string {
	label := attentionLabel(t.Attention)
	if t.Attention == db.AttentionOK {
		return label
	}
	if tp, ok := m.taskProcesses[t.ID]; ok && len(tp.Children) > 0 {
		label += fmt.Sprintf("⚙%d", len(tp.Children))
	}
	return label
}

func (m Model) renderStatusBar() string {
	var hints []string

	hints = append(hints, "[n]ew")
	if m.selectedTask() != nil {
		hints = append(hints, "[enter]focus", "[tab]detail")
	}
	hints = append(hints, "[/]filter", "[?]help", "[q]uit")

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
	return `Global Keys

  n         Create new task
  i         Import existing Claude session
  Enter     Focus selected task window
  Tab       Open task detail modal
  d         Complete task (with confirmation)
  j/k       Navigate up/down
  s         Toggle sort (created / priority)
  S         Sit rep (briefing on all active tasks)
  r         Refresh AI summaries
  C         Compact windows (renumber sequentially)
  /         Filter tasks (esc to clear)
  ?         Toggle this help
  q         Quit krang (tasks keep running)

Detail Modal Keys (press Tab on a task)

  p         Park / unpark (toggles based on state)
  f         Freeze / unfreeze (toggles based on state)
  d         Complete task
  +         Create companion window
  F         Edit task flags (sandbox, permissions)
  W         Add repos to workspace task
  Enter     Focus task window
  Esc/Tab   Close modal

Task States

  active    Running in krang's tmux session. Claude Code is
            actively working or waiting for input.
  parked    Moved to a background tmux session. Claude is
            still running but not visible. Park tasks to
            reduce clutter.
  frozen    No tmux window. Session ID saved so Claude can
            resume with --resume. Use this for tasks you want
            to pause without using resources.

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
    A shell window (<name>+) associated with a task. Created
    with + in the detail modal. Travels with the task on
    park/unpark and is destroyed on freeze.

  Window naming
    <name>   Task window (identified by @krang-task option)
    <name>+  Companion window (identified by @krang-companion)

  Sort modes
    Created (default) shows all tasks in creation order.
    Priority shows only active tasks sorted by attention
    urgency: PERM > ERR > wait > ok > done.`
}

func (m Model) renderConfirmComplete(t *db.Task) string {
	var content strings.Builder

	content.WriteString(m.styles.ModalTitle.Render(fmt.Sprintf("Complete %q?", t.Name)))
	content.WriteString("\n\n")
	content.WriteString(m.styles.ModalContent.Render("  • Claude process will be stopped"))
	if t.WorkspaceDir != "" {
		content.WriteString("\n")
		wsPath := tildeify(t.WorkspaceDir)
		content.WriteString(m.styles.ModalContent.Render(fmt.Sprintf("  • Workspace at %s will be deleted", wsPath)))
	}
	content.WriteString("\n\n")
	content.WriteString(m.styles.ModalContent.Render("          [y] Confirm  [n] Cancel"))

	modalWidth := m.width / 2
	if modalWidth < 44 {
		modalWidth = 44
	}
	if modalWidth > m.width-4 {
		modalWidth = m.width - 4
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.theme.Error).
		Padding(1, 2).
		Width(modalWidth)

	return box.Render(content.String())
}

func (m Model) renderDetailModal(t *db.Task) string {
	var content strings.Builder

	// Header: name + state + attention
	stateStr := stateLabel(t.State)
	attnStr := m.attentionWithProcs(*t)
	header := fmt.Sprintf("%s  [%s]  %s", t.Name, stateStr, attnStr)
	content.WriteString(m.styles.ModalTitle.Render(header))
	content.WriteString("\n")

	// Info section
	if t.Cwd != "" {
		content.WriteString(m.styles.ModalContent.Render("  cwd: " + relativeCwd(t.Cwd)))
		content.WriteString("\n")
	}
	if !t.CreatedAt.IsZero() {
		age := time.Since(t.CreatedAt).Truncate(time.Second)
		content.WriteString(m.styles.ModalContent.Render("  age: " + age.String()))
		content.WriteString("\n")
	}
	if t.Flags.HasNonDefault() {
		var flags []string
		if t.Flags.NoSandbox {
			flags = append(flags, "no-sandbox")
		}
		if t.Flags.DangerouslySkipPermissions {
			flags = append(flags, "skip-perms")
		}
		if t.Flags.Debug {
			flags = append(flags, "debug")
		}
		content.WriteString(m.styles.ModalContent.Render("  flags: " + strings.Join(flags, ", ")))
		content.WriteString("\n")
	}

	// Process section
	if tp, ok := m.taskProcesses[t.ID]; ok && len(tp.Children) > 0 {
		content.WriteString("\n")
		content.WriteString(m.styles.ModalContent.Render(fmt.Sprintf("  Background processes (%d):", len(tp.Children))))
		content.WriteString("\n")
		for _, child := range tp.Children {
			content.WriteString(m.styles.ModalContent.Render("    " + child.Command))
			content.WriteString("\n")
		}
	}

	// Actions section
	content.WriteString("\n")
	content.WriteString(m.styles.ModalTitle.Render("Actions"))
	content.WriteString("\n")

	type action struct {
		key  string
		desc string
	}
	var actions []action

	switch t.State {
	case db.StateActive:
		actions = append(actions,
			action{"enter", "Focus task window"},
			action{"p", "Park"},
			action{"f", "Freeze"},
			action{"d", "Complete"},
			action{"+", "Create companion"},
		)
	case db.StateParked:
		actions = append(actions,
			action{"p", "Unpark"},
			action{"f", "Freeze"},
			action{"d", "Complete"},
		)
	case db.StateDormant:
		actions = append(actions,
			action{"f", "Unfreeze"},
			action{"d", "Complete"},
		)
	}

	if t.State != db.StateCompleted && t.State != db.StateFailed {
		actions = append(actions, action{"F", "Edit flags"})
	}
	if t.WorkspaceDir != "" && m.repoSets != nil &&
		m.repoSets.WorkspaceStrategy == workspace.StrategyMultiRepo {
		actions = append(actions, action{"W", "Add repos"})
	}

	for _, a := range actions {
		line := fmt.Sprintf("  %-6s %s", a.key, a.desc)
		content.WriteString(m.styles.ModalContent.Render(line))
		content.WriteString("\n")
	}

	content.WriteString("\n")
	content.WriteString(m.styles.ModalContent.Render("  esc/tab  Close"))

	modalWidth := m.width / 2
	if modalWidth < 40 {
		modalWidth = 40
	}
	if modalWidth > m.width-4 {
		modalWidth = m.width - 4
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.ModalBorder).
		Padding(1, 2).
		Width(modalWidth)

	return box.Render(content.String())
}


func (m Model) renderWorkspaceProgress() string {
	var content strings.Builder
	for i, line := range m.workspaceProgressLines {
		if i == 0 {
			content.WriteString(m.styles.ModalTitle.Render(line))
		} else {
			content.WriteString(m.styles.ModalContent.Render(line))
		}
		content.WriteString("\n")
	}

	modalWidth := m.width / 2
	if modalWidth < 40 {
		modalWidth = 40
	}
	if modalWidth > m.width-4 {
		modalWidth = m.width - 4
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.ModalBorder).
		Padding(1, 2).
		Width(modalWidth)

	return box.Render(content.String())
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
