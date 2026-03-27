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
	case ModeCommandPalette:
		modalContent = m.renderCommandPalette()
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

	// Action bar shown in normal mode and modal overlay modes (where
	// it's visible behind the overlay).
	showHints := m.mode == ModeNormal || m.mode == ModeDetail ||
		m.mode == ModeConfirmComplete || m.mode == ModeCommandPalette
	if showHints {
		if m.filterText != "" {
			top.WriteString(m.styles.Header.Render(fmt.Sprintf("  filter: %s (/ to change, esc to clear)", m.filterText)))
		}
		top.WriteString("\n")
		top.WriteString(m.renderActionBar())
	}

	background := m.pinToBottom(top.String())

	if modalContent != "" {
		return overlayCenter(background, modalContent, m.width, m.height)
	}
	return background
}

// renderNormalView renders the standard view (header, table, status bar,
// debug log) used as the background behind modal overlays.
func (m Model) renderNormalView() string {
	var top strings.Builder
	top.WriteString(m.renderHeader())
	top.WriteString("\n\n")
	top.WriteString(m.renderTable())
	top.WriteString("\n")
	if m.filterText != "" {
		top.WriteString(m.styles.Header.Render(fmt.Sprintf("  filter: %s (/ to change, esc to clear)", m.filterText)))
	}
	top.WriteString("\n")
	top.WriteString(m.renderActionBar())
	return m.pinToBottom(top.String())
}

// pinToBottom pads between the top content and the debug log + footer
// to pin them to the bottom of the terminal.
func (m Model) pinToBottom(topStr string) string {
	bottom := m.renderDebugLog()
	footer := m.renderFooter()
	topLines := strings.Count(topStr, "\n") + 1
	bottomLines := maxVisibleLogLines + 3 + 1 // +1 for footer
	gap := m.height - topLines - bottomLines
	if gap < 0 {
		gap = 0
	}
	return topStr + strings.Repeat("\n", gap) + bottom + "\n" + footer
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
	sep := m.styles.Header.Render(" | ")
	stats := fmt.Sprintf("Active: %d", activeCt) +
		sep +
		m.styles.StateParked.Render(fmt.Sprintf("Parked: %d", parkedCt)) +
		sep +
		m.styles.StateDormant.Render(fmt.Sprintf("Frozen: %d", dormantCt))
	if sortIndicator != "" {
		stats += m.styles.Header.Render(sortIndicator)
	}

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
			if op != "" {
				attn = m.spinner.View() + " " + op
			} else {
				attn = m.spinner.View()
			}
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

	tableContent := t.Render()

	// Wrap table in a box with custom bottom border containing hints.
	borderColor := m.styles.theme.Border
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	innerWidth := m.width - 4 // border (2) + padding (2)

	// Top border.
	topBorder := borderStyle.Render("╭" + strings.Repeat("─", innerWidth+2) + "╮")

	// Table body with side borders.
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		BorderTop(false).
		BorderBottom(false).
		Padding(0, 1).
		Width(m.width - 2)
	middle := boxStyle.Render(tableContent)

	// Bottom border with right-justified hints.
	hints := []string{
		m.renderHint("/", "filter"),
		m.renderHint("s", "sort"),
		m.renderHint("j/k", "nav"),
	}

	taskCount := len(tasks)
	totalCount := len(m.tasks)
	countStyle := lipgloss.NewStyle().Foreground(m.styles.theme.Muted)
	var countStr string
	if m.filterText != "" {
		countStr = countStyle.Render(fmt.Sprintf("%d/%d", taskCount, totalCount))
	} else {
		countStr = countStyle.Render(fmt.Sprintf("%d tasks", totalCount))
	}
	if m.sortByPriority {
		countStr += countStyle.Render(" · priority")
	}

	hintsStr := " " + strings.Join(hints, "  ") + "  " + countStr + " "
	hintsWidth := lipgloss.Width(hintsStr)
	lineLen := innerWidth + 2 - hintsWidth
	if lineLen < 1 {
		lineLen = 1
	}
	bottomBorder := borderStyle.Render("╰"+strings.Repeat("─", lineLen)) +
		hintsStr +
		borderStyle.Render("╯")

	return topBorder + "\n" + middle + "\n" + bottomBorder
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

// renderHint renders a "key label" hint with the key in accent color
// and the label in muted color.
func (m Model) renderHint(key, label string) string {
	keyStyle := lipgloss.NewStyle().Foreground(m.styles.theme.Accent)
	labelStyle := lipgloss.NewStyle().Foreground(m.styles.theme.Muted)
	return keyStyle.Render(key) + " " + labelStyle.Render(label)
}

func (m Model) renderActionBar() string {
	hints := []string{m.renderHint("n", "new")}
	if m.selectedTask() != nil {
		hints = append(hints,
			m.renderHint("enter", "focus"),
			m.renderHint("tab", "detail"),
			m.renderHint("c", "complete"),
		)
	}
	return " " + strings.Join(hints, "  ")
}

func (m Model) renderFooter() string {
	hints := []string{
		m.renderHint(":", "command"),
		m.renderHint("?", "help"),
		m.renderHint("q", "quit"),
	}
	return " " + strings.Join(hints, "  ")
}

func (m Model) renderCommandPalette() string {
	cmds := paletteCommands(m)

	var content strings.Builder
	content.WriteString(m.styles.ModalTitle.Render("Commands"))
	content.WriteString("\n\n")

	for i, cmd := range cmds {
		cursor := "  "
		if i == m.paletteCursor {
			cursor = "> "
		}
		nameStyle := m.styles.ModalContent
		if i == m.paletteCursor {
			nameStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(m.styles.theme.Accent)
		}
		line := cursor + nameStyle.Render(cmd.Name) +
			m.styles.ModalContent.Render("  "+cmd.Desc)
		content.WriteString(line)
		if i < len(cmds)-1 {
			content.WriteString("\n")
		}
	}

	content.WriteString("\n\n")
	content.WriteString("  " + m.renderHint("j/k", "navigate") + "  " +
		m.renderHint("enter", "run") + "  " +
		m.renderHint("esc", "close"))

	modalWidth := m.width / 2
	if modalWidth < 50 {
		modalWidth = 50
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
	modalWidth := m.width * 2 / 3
	if modalWidth < 50 {
		modalWidth = 50
	}
	if modalWidth > m.width-4 {
		modalWidth = m.width - 4
	}

	// Inner width accounts for border (2) + padding (4).
	innerWidth := modalWidth - 6

	footerHints := "  " + m.renderHint("q/esc/?", "Close") + "    " + m.renderHint("j/k", "Scroll")
	scrollPct := ""
	if m.helpViewport.TotalLineCount() > m.helpViewport.VisibleLineCount() {
		scrollPct = fmt.Sprintf("%.0f%%", m.helpViewport.ScrollPercent()*100)
	}
	footer := fmt.Sprintf("%-*s%s", innerWidth-len(scrollPct), footerHints, scrollPct)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.styles.ModalBorder).
		Padding(0, 2).
		Width(modalWidth)

	content := box.Render(m.helpViewport.View() + "\n" + footer)

	// Render the normal background and overlay the help modal.
	bg := m.renderNormalView()
	return overlayCenter(bg, content, m.width, m.height)
}

func (m Model) buildHelpContent() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(m.styles.theme.Title)
	desc := lipgloss.NewStyle().Foreground(m.styles.theme.Muted)
	subtitle := lipgloss.NewStyle().Italic(true).Foreground(m.styles.theme.Muted)

	type hint struct{ key, label string }
	renderSection := func(hints []hint) string {
		var sb strings.Builder
		for _, h := range hints {
			sb.WriteString("  " + m.renderHint(fmt.Sprintf("%-8s", h.key), h.label) + "\n")
		}
		return sb.String()
	}

	var sb strings.Builder

	sb.WriteString(title.Render("Global Keys") + "\n\n")
	sb.WriteString(renderSection([]hint{
		{"n", "Create new task"},
		{"enter", "Focus selected task window"},
		{"tab", "Open task detail modal"},
		{"c", "Complete task (with confirmation)"},
		{"j/k", "Navigate up/down"},
		{"s", "Toggle sort (created / priority)"},
		{"/", "Filter tasks (esc to clear)"},
		{":", "Command palette (sit rep, import, compact)"},
		{"?", "Toggle this help"},
		{"q", "Quit krang (tasks keep running)"},
	}))

	sb.WriteString("\n" + title.Render("Detail Modal Keys") + "\n")
	sb.WriteString(subtitle.Render("  Press Tab on a task to open") + "\n\n")
	sb.WriteString(renderSection([]hint{
		{"p", "Park / unpark (toggles based on state)"},
		{"f", "Freeze / unfreeze (toggles based on state)"},
		{"c", "Complete task"},
		{"+", "Create companion window"},
		{"F", "Edit task flags (sandbox, permissions)"},
		{"W", "Add repos to workspace task"},
		{"enter", "Focus task window"},
		{"esc/tab", "Close modal"},
	}))

	sb.WriteString("\n" + title.Render("Task States") + "\n\n")
	for _, item := range []hint{
		{"active", "Running in krang's tmux session. Claude is working or waiting for input."},
		{"parked", "Moved to a background tmux session. Claude keeps running but is out of sight."},
		{"frozen", "Tmux window destroyed, session ID saved. No resources used. Resume later with --resume."},
	} {
		sb.WriteString("  " + m.renderHint(fmt.Sprintf("%-8s", item.key), item.label) + "\n")
	}

	sb.WriteString("\n" + title.Render("Attention States") + "\n\n")
	for _, item := range []hint{
		{"ok", "Claude is working normally."},
		{"wait", "Claude stopped and is waiting for your input."},
		{"PERM", "A permission prompt is blocking Claude."},
		{"ERR", "Something went wrong (e.g. stop failure)."},
	} {
		sb.WriteString("  " + m.renderHint(fmt.Sprintf("%-8s", item.key), item.label) + "\n")
	}

	sb.WriteString("\n" + title.Render("Glossary") + "\n\n")
	accentStyle := lipgloss.NewStyle().Foreground(m.styles.theme.Accent)
	for _, item := range []struct{ term, def string }{
		{"Companion window", "A shell window (<name>+) tied to a task. Travels on park/unpark, destroyed on freeze."},
		{"Workspace", "An isolated directory (clone or jj workspace) created per task. Deleted on complete."},
		{"Sandbox", "A wrapper command (e.g. bwrap) that confines Claude to limited filesystem/network access."},
	} {
		sb.WriteString("  " + accentStyle.Render(item.term) + " " + desc.Render(item.def) + "\n")
	}

	return sb.String()
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
	content.WriteString("          " + m.renderHint("y", "Confirm") + "  " + m.renderHint("n", "Cancel"))

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
	if t.SessionID != "" {
		content.WriteString(m.styles.ModalContent.Render("  claude session: " + t.SessionID))
		content.WriteString("\n")
	}
	if t.TmuxWindow != "" {
		session := m.activeSession
		if t.State == db.StateParked {
			session = m.parkedSession
		}
		companions := len(tmux.FindCompanions(session, t.Name))
		if companions > 0 {
			content.WriteString(m.styles.ModalContent.Render(fmt.Sprintf("  companions: %d", companions)))
			content.WriteString("\n")
		}
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
	if t.Summary != "" {
		content.WriteString(m.styles.ModalContent.Render("  summary: " + t.Summary))
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
			action{"c", "Complete"},
			action{"+", "Create companion"},
		)
	case db.StateParked:
		actions = append(actions,
			action{"p", "Unpark"},
			action{"f", "Freeze"},
			action{"c", "Complete"},
		)
	case db.StateDormant:
		actions = append(actions,
			action{"f", "Unfreeze"},
			action{"c", "Complete"},
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
		content.WriteString("  " + m.renderHint(fmt.Sprintf("%-6s", a.key), a.desc))
		content.WriteString("\n")
	}

	content.WriteString("\n")
	content.WriteString("  " + m.renderHint("esc/tab", "Close"))

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

	timestampStyle := lipgloss.NewStyle().Foreground(m.styles.theme.Accent).Faint(true)

	var content strings.Builder
	for i := 0; i < maxVisibleLogLines; i++ {
		if i < len(lines) {
			line := lines[i]
			// Color the [HH:MM:SS] timestamp differently from the message.
			if len(line) > 10 && line[0] == '[' {
				if end := strings.Index(line, "]"); end > 0 {
					content.WriteString(timestampStyle.Render(line[:end+1]))
					content.WriteString(m.styles.DebugLog.Render(line[end+1:]))
				} else {
					content.WriteString(m.styles.DebugLog.Render(line))
				}
			} else {
				content.WriteString(m.styles.DebugLog.Render(line))
			}
		}
		if i < maxVisibleLogLines-1 {
			content.WriteString("\n")
		}
	}

	borderColor := m.styles.Header.GetForeground()
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	labelStyle := lipgloss.NewStyle().Foreground(m.styles.theme.Accent)
	innerWidth := m.width - 4 // account for left/right border + padding

	// Build top border with embedded label.
	label := " Events "
	lineLen := innerWidth - lipgloss.Width(label)
	if lineLen < 0 {
		lineLen = 0
	}
	topBorder := borderStyle.Render("╭─") + labelStyle.Render(label) + borderStyle.Render(strings.Repeat("─", lineLen)+"╮")

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		BorderTop(false).
		Padding(0, 1).
		Width(m.width - 2)

	return topBorder + "\n" + boxStyle.Render(content.String())
}
