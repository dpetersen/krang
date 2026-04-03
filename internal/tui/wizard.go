package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/github"
	"github.com/dpetersen/krang/internal/workspace"
)

// wizardTab identifies which tab is active in the task creation wizard.
type wizardTab int

const (
	wizardTabName    wizardTab = iota
	wizardTabRepos             // repos/CWD picker; may be skipped
	wizardTabOptions           // sandbox + flags; optional
)

// wizardSubmitMsg is emitted when the wizard is ready to create a task.
type wizardSubmitMsg struct {
	Name           string
	Cwd            string
	Flags          db.TaskFlags
	SandboxProfile string
	SelectedRepos  []string
}

// wizardEditSubmitMsg is emitted when the edit wizard is ready to save changes.
type wizardEditSubmitMsg struct {
	TaskID         string
	Flags          db.TaskFlags
	SandboxProfile string
	SelectedRepos  []string
}

// wizardCloneRemoteMsg is emitted when the user selects a remote repo to clone.
type wizardCloneRemoteMsg struct {
	Org  string
	Repo string
}

// wizardCancelMsg is emitted when the user presses esc.
type wizardCancelMsg struct{}

// taskWizard is the tabbed task creation/edit component.
type taskWizard struct {
	activeTab    wizardTab
	editMode     bool   // true when editing an existing task
	editTaskID   string // task ID being edited (edit mode only)
	skipReposTab bool   // true when non-workspace with only "." as CWD

	// Tab 1: Name (create mode only)
	nameForm  *huh.Form
	nameValue string

	// Tab 2: Repos/CWD (exactly one approach active)
	repoPicker *tabbedRepoPicker // multi_repo or single_repo w/o local repos
	reposForm  *huh.Form         // single_repo with local repos
	cwdForm    *huh.Form         // non-workspace with subdirs
	reposValue string            // selected repo name
	cwdValue   string            // selected CWD

	// Tab 3: Options
	optionsForm   *huh.Form
	sandboxValue  string
	flagValues    []string
	hasOptionsTab bool // false when no sandbox profiles AND we want to hide it

	// Remote search state
	lastSearchQuery string

	// Context for submission
	repoSets       *workspace.RepoSets
	validateName   func(string) error
	defaultSandbox string
	baseDir        string
	styles         Styles
	theme          Theme
	huhTheme       *huh.Theme
	width          int
}

func newTaskWizard(
	nameInUse func(string) bool,
	repoSets *workspace.RepoSets,
	sandboxProfiles []string,
	defaultSandbox string,
	baseDir string,
	styles Styles,
	theme Theme,
	huhTheme *huh.Theme,
) *taskWizard {
	w := &taskWizard{
		activeTab:      wizardTabName,
		repoSets:       repoSets,
		validateName:   validateTaskName(nameInUse),
		defaultSandbox: defaultSandbox,
		baseDir:        baseDir,
		styles:         styles,
		theme:          theme,
		huhTheme:       huhTheme,
		hasOptionsTab:  true,
	}

	// Tab 1: Name form.
	w.nameForm = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Task name").
				Placeholder("my-task").
				CharLimit(40).
				Validate(w.validateName).
				Value(&w.nameValue),
		),
	).WithTheme(huhTheme).WithShowHelp(false)
	// No SubmitCmd/CancelCmd — the wizard intercepts enter/esc before
	// they reach huh, so the form stays alive for re-editing.

	// Tab 2: depends on workspace config.
	w.buildTab2(repoSets, baseDir, styles, theme, huhTheme)

	// Tab 3: Options form.
	w.buildOptionsForm(sandboxProfiles, defaultSandbox, huhTheme)

	return w
}

func newEditWizard(
	task *db.Task,
	repoSets *workspace.RepoSets,
	sandboxProfiles []string,
	defaultSandbox string,
	styles Styles,
	theme Theme,
	huhTheme *huh.Theme,
	excludeRepos map[string]bool,
) *taskWizard {
	w := &taskWizard{
		editMode:       true,
		editTaskID:     task.ID,
		repoSets:       repoSets,
		defaultSandbox: defaultSandbox,
		baseDir:        task.Cwd,
		styles:         styles,
		theme:          theme,
		huhTheme:       huhTheme,
		hasOptionsTab:  true,
	}

	// Determine if we have a repos tab.
	hasRepos := repoSets != nil && repoSets.WorkspaceStrategy != "" && task.WorkspaceDir != ""
	if hasRepos {
		w.activeTab = wizardTabRepos
		w.buildTab2ForEdit(repoSets, styles, theme, huhTheme, excludeRepos)
	} else {
		w.skipReposTab = true
		w.activeTab = wizardTabOptions
	}

	// Pre-populate flags from current task state.
	currentSandbox := task.SandboxProfile
	if currentSandbox == "" {
		currentSandbox = defaultSandbox
	}
	// Only convert to the UI's "(none)" label when profiles exist and will
	// be shown. When no profiles exist, the select field is hidden and the
	// value passes through unchanged.
	if len(sandboxProfiles) > 0 && (currentSandbox == "none" || currentSandbox == "") {
		currentSandbox = sandboxNone
	}
	var currentFlags []string
	if task.Flags.DangerouslySkipPermissions {
		currentFlags = append(currentFlags, "skip_perms")
	}
	if task.Flags.Debug {
		currentFlags = append(currentFlags, "debug")
	}
	w.buildOptionsFormWithDefaults(sandboxProfiles, currentSandbox, currentFlags, huhTheme)

	return w
}

func (w *taskWizard) buildTab2ForEdit(rs *workspace.RepoSets, styles Styles, theme Theme, huhTheme *huh.Theme, excludeRepos map[string]bool) {
	repos, _ := rs.ListRepos()
	ghAvail := github.IsAvailable()
	picker := newTabbedRepoPicker("Select repos to add:", rs.Sets, repos, styles, rs, ghAvail)
	picker.excludeRepos = excludeRepos
	w.repoPicker = &picker
}

func (w *taskWizard) buildTab2(rs *workspace.RepoSets, baseDir string, styles Styles, theme Theme, huhTheme *huh.Theme) {
	if rs != nil && rs.WorkspaceStrategy != "" {
		repos, _ := rs.ListRepos()
		singleRepo := rs.WorkspaceStrategy == workspace.StrategySingleRepo
		hasLocalRepos := len(repos) > 0

		if singleRepo && hasLocalRepos {
			// Single-repo with local repos: huh Select.
			w.reposValue = ""
			repoOptions := []huh.Option[string]{
				huh.NewOption("(none — empty workspace)", ""),
			}
			for _, r := range repos {
				repoOptions = append(repoOptions, huh.NewOption(r, r))
			}
			w.reposForm = huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("Select repo").
						Options(repoOptions...).
						Value(&w.reposValue),
				),
			).WithTheme(huhTheme).WithShowHelp(false)
		} else {
			// Multi-repo or single-repo with no local repos: full repo picker.
			ghAvail := github.IsAvailable()
			picker := newTabbedRepoPicker("Select repos:", rs.Sets, repos, styles, rs, ghAvail)
			w.repoPicker = &picker
		}
		return
	}

	// Non-workspace: CWD picker.
	dirOptions := cwdOptions(baseDir)
	if len(dirOptions) <= 1 {
		// Only "." — skip repos tab entirely.
		w.skipReposTab = true
		w.cwdValue = baseDir
		return
	}

	w.cwdValue = "."
	w.cwdForm = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Working directory").
				Options(dirOptions...).
				Value(&w.cwdValue),
		),
	).WithTheme(huhTheme).WithShowHelp(false)
}

func (w *taskWizard) buildOptionsFormWithDefaults(sandboxProfiles []string, sandboxDefault string, flagDefaults []string, huhTheme *huh.Theme) {
	w.sandboxValue = sandboxDefault
	w.flagValues = flagDefaults
	w.buildOptionsFormInner(sandboxProfiles, huhTheme)
}

func (w *taskWizard) buildOptionsForm(sandboxProfiles []string, defaultSandbox string, huhTheme *huh.Theme) {
	w.sandboxValue = defaultSandbox
	w.buildOptionsFormInner(sandboxProfiles, huhTheme)
}

func (w *taskWizard) buildOptionsFormInner(sandboxProfiles []string, huhTheme *huh.Theme) {
	var fields []huh.Field

	if len(sandboxProfiles) > 0 {
		profileOptions := sandboxProfileOptions(sandboxProfiles)
		fields = append(fields,
			huh.NewSelect[string]().
				Title("Sandbox profile").
				Options(profileOptions...).
				Value(&w.sandboxValue),
		)
	}

	fields = append(fields,
		huh.NewMultiSelect[string]().
			Title("Flags").
			Options(
				huh.NewOption("Skip Permissions — --dangerously-skip-permissions", "skip_perms"),
				huh.NewOption("Debug — export KRANG_DEBUG=1 for relay logging", "debug"),
			).
			Value(&w.flagValues),
	)

	w.optionsForm = huh.NewForm(
		huh.NewGroup(fields...),
	).WithTheme(huhTheme).WithShowHelp(false)
}

// Init returns the initial command for the wizard.
func (w *taskWizard) Init() tea.Cmd {
	if w.editMode {
		return w.switchToTab(w.activeTab)
	}
	return w.nameForm.Init()
}

// Update handles key messages for the wizard.
func (w *taskWizard) Update(msg tea.Msg) (tea.Cmd, tea.Msg) {
	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		// Forward non-key messages to the active form.
		return w.updateActiveForm(msg)
	}

	// [ and ] navigate between tabs. These are illegal in task names
	// so they don't conflict with name input. Skip when repo picker
	// text inputs are active (filter/search).
	textInputActive := w.activeTab == wizardTabRepos && w.repoPicker != nil && w.repoPicker.isTextInputActive()
	if !textInputActive {
		switch keyMsg.String() {
		case "[":
			return w.prevTab(), nil
		case "]":
			if !w.editMode && w.activeTab == wizardTabName && w.validateName(w.nameValue) != nil {
				// Forward as enter so huh shows the validation error.
				return w.updateActiveForm(tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))
			}
			return w.nextTab(), nil
		}
	}

	// Esc handling: for the repo picker, delegate to tab-specific handlers
	// which manage contextual back-out (clear filter, exit search phase, etc.).
	// Only cancel the wizard if there's nothing to back out of.
	if keyMsg.String() == "esc" {
		if w.activeTab == wizardTabRepos && w.repoPicker != nil {
			// Delegate to the repo picker tab handlers — they return
			// wizardCancelMsg only when there's nothing left to back out of.
			return w.updateRepoPickerTab(keyMsg)
		}
		return nil, wizardCancelMsg{}
	}

	// Tab 1 (Name): intercept enter so huh never "completes" the form.
	if w.activeTab == wizardTabName {
		if keyMsg.String() == "enter" {
			if err := w.validateName(w.nameValue); err != nil {
				// Let huh show the validation error by forwarding.
				return w.updateActiveForm(msg)
			}
			if w.skipReposTab {
				return nil, w.buildSubmitMsg()
			}
			return w.switchToTab(wizardTabRepos), nil
		}
		return w.updateActiveForm(msg)
	}

	// Tab 2 with repo picker: delegate most keys.
	if w.activeTab == wizardTabRepos && w.repoPicker != nil {
		return w.updateRepoPickerTab(keyMsg)
	}

	// Tab 2 with huh form: intercept enter to submit the wizard.
	if w.activeTab == wizardTabRepos && keyMsg.String() == "enter" {
		return nil, w.buildSubmitMsg()
	}

	// Tab 3: enter always submits. Use tab/shift-tab and space to
	// navigate and toggle within the form.
	if w.activeTab == wizardTabOptions && keyMsg.String() == "enter" {
		return nil, w.buildSubmitMsg()
	}

	// Forward remaining keys to active form.
	return w.updateActiveForm(msg)
}

func (w *taskWizard) tabOrder() []wizardTab {
	if w.editMode {
		if w.skipReposTab {
			return []wizardTab{wizardTabOptions}
		}
		return []wizardTab{wizardTabRepos, wizardTabOptions}
	}
	if w.skipReposTab {
		return []wizardTab{wizardTabName, wizardTabOptions}
	}
	return []wizardTab{wizardTabName, wizardTabRepos, wizardTabOptions}
}

func (w *taskWizard) prevTab() tea.Cmd {
	tabs := w.tabOrder()
	for i, t := range tabs {
		if t == w.activeTab && i > 0 {
			return w.switchToTab(tabs[i-1])
		}
	}
	return nil
}

func (w *taskWizard) nextTab() tea.Cmd {
	tabs := w.tabOrder()
	for i, t := range tabs {
		if t == w.activeTab && i < len(tabs)-1 {
			return w.switchToTab(tabs[i+1])
		}
	}
	return nil
}

func (w *taskWizard) switchToTab(tab wizardTab) tea.Cmd {
	w.activeTab = tab
	switch tab {
	case wizardTabName:
		return w.nameForm.Init()
	case wizardTabRepos:
		if w.repoPicker != nil {
			return nil
		}
		if w.reposForm != nil {
			return w.reposForm.Init()
		}
		if w.cwdForm != nil {
			return w.cwdForm.Init()
		}
	case wizardTabOptions:
		return w.optionsForm.Init()
	}
	return nil
}

func (w *taskWizard) updateActiveForm(msg tea.Msg) (tea.Cmd, tea.Msg) {
	var form *huh.Form
	switch w.activeTab {
	case wizardTabName:
		form = w.nameForm
	case wizardTabRepos:
		if w.reposForm != nil {
			form = w.reposForm
		} else if w.cwdForm != nil {
			form = w.cwdForm
		}
	case wizardTabOptions:
		form = w.optionsForm
	}
	if form == nil {
		return nil, nil
	}

	model, cmd := form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		switch w.activeTab {
		case wizardTabName:
			w.nameForm = f
		case wizardTabRepos:
			if w.reposForm != nil {
				w.reposForm = f
			} else if w.cwdForm != nil {
				w.cwdForm = f
			}
		case wizardTabOptions:
			w.optionsForm = f
		}
	}

	// Check if the form produced a wizard message via its Cmd.
	return cmd, nil
}

func (w *taskWizard) updateRepoPickerTab(msg tea.KeyMsg) (tea.Cmd, tea.Msg) {
	tp := w.repoPicker

	// Handle tab key for Local/Remote switching within the picker.
	if msg.String() == "tab" {
		if tp.activeTab == pickerTabLocal {
			tp.switchToRemote()
		} else {
			tp.switchToLocal()
		}
		return nil, nil
	}

	// Esc on Local tab: clear filter first, then cancel wizard.
	if msg.String() == "esc" && tp.activeTab == pickerTabLocal {
		if tp.local.filtering {
			tp.local.filtering = false
			tp.local.filter.Blur()
			return nil, nil
		}
		if strings.TrimSpace(tp.local.filter.Value()) != "" {
			tp.local.filter.Reset()
			tp.local.refilter()
			return nil, nil
		}
		return nil, wizardCancelMsg{}
	}

	// Esc on Remote tab: handled by handleRepoPickerRemoteKey which
	// backs out through phases (search → org select → cancel wizard).

	// Handle enter on local tab (when not filtering) — submit the wizard.
	if msg.String() == "enter" && tp.activeTab == pickerTabLocal && !tp.local.filtering {
		return nil, w.buildSubmitMsg()
	}

	// Delegate everything else to the repo picker's local/remote key handlers.
	if tp.activeTab == pickerTabRemote {
		return w.handleRepoPickerRemoteKey(msg)
	}
	return w.handleRepoPickerLocalKey(msg)
}

func (w *taskWizard) handleRepoPickerLocalKey(msg tea.KeyMsg) (tea.Cmd, tea.Msg) {
	p := &w.repoPicker.local

	if p.filtering {
		switch msg.String() {
		case "esc":
			p.filtering = false
			p.filter.Blur()
			return nil, nil
		case "enter":
			p.filtering = false
			p.filter.Blur()
			return nil, nil
		}
		cmd := p.updateFilter(msg)
		return cmd, nil
	}

	switch msg.String() {
	case "j", "down":
		p.moveDown()
	case "k", "up":
		p.moveUp()
	case " ":
		p.toggle()
	case "/":
		p.filtering = true
		p.filter.Focus()
		return p.filter.Cursor.BlinkCmd(), nil
	}
	return nil, nil
}

func (w *taskWizard) handleRepoPickerRemoteKey(msg tea.KeyMsg) (tea.Cmd, tea.Msg) {
	r := &w.repoPicker.remote
	key := msg.String()

	// --- Esc: contextual back-out through phases ---
	if key == "esc" {
		switch r.phase {
		case remotePhaseSearch:
			r.searchInput.Blur()
			r.results = nil
			r.err = nil
			r.cursor = 0
			if len(r.configOrgs) > 0 {
				r.phase = remotePhaseOrgSelect
			} else {
				r.phase = remotePhaseOrgEntry
				r.orgInput.Focus()
				return r.orgInput.Cursor.BlinkCmd(), nil
			}
			return nil, nil
		case remotePhaseOrgEntry:
			r.orgInput.Blur()
			if len(r.configOrgs) > 0 {
				r.phase = remotePhaseOrgSelect
				return nil, nil
			}
			// No org select to go back to — cancel wizard.
			return nil, wizardCancelMsg{}
		default:
			// Org select phase — cancel wizard.
			return nil, wizardCancelMsg{}
		}
	}

	// --- Phase-specific key handling ---
	switch r.phase {
	case remotePhaseOrgSelect:
		switch key {
		case "j", "down":
			if r.cursor < len(r.configOrgs) { // +1 for "Other..."
				r.cursor++
			}
		case "k", "up":
			if r.cursor > 0 {
				r.cursor--
			}
		case "enter":
			if r.cursor < len(r.configOrgs) {
				r.activeOrg = r.configOrgs[r.cursor]
				r.phase = remotePhaseSearch
				r.cursor = 0
				r.searchInput.Focus()
				return r.searchInput.Cursor.BlinkCmd(), nil
			}
			// "Other..." selected.
			r.phase = remotePhaseOrgEntry
			r.orgInput.Focus()
			return r.orgInput.Cursor.BlinkCmd(), nil
		}
		return nil, nil

	case remotePhaseOrgEntry:
		// All keys go to the org text input.
		if key == "enter" {
			org := strings.TrimSpace(r.orgInput.Value())
			if org != "" {
				r.activeOrg = org
				r.phase = remotePhaseSearch
				r.cursor = 0
				r.orgInput.Blur()
				r.searchInput.Focus()
				return r.searchInput.Cursor.BlinkCmd(), nil
			}
			return nil, nil
		}
		var cmd tea.Cmd
		r.orgInput, cmd = r.orgInput.Update(msg)
		return cmd, nil

	case remotePhaseSearch:
		// Up/down navigate results; all other keys go to the search input.
		switch key {
		case "up":
			if r.cursor > 0 {
				r.cursor--
			}
			return nil, nil
		case "down":
			if r.cursor < len(r.results)-1 {
				r.cursor++
			}
			return nil, nil
		case "enter":
			if r.cursor < len(r.results) {
				return nil, wizardCloneRemoteMsg{
					Org:  r.activeOrg,
					Repo: r.results[r.cursor],
				}
			}
			return nil, nil
		}
		// Everything else (including j, k, letters) goes to search input.
		var cmd tea.Cmd
		r.searchInput, cmd = r.searchInput.Update(msg)
		return cmd, nil
	}

	return nil, nil
}

func (w *taskWizard) resolvedSandboxProfile() string {
	if w.sandboxValue == "" {
		return ""
	}
	if w.sandboxValue == sandboxNone {
		return "none"
	}
	return w.sandboxValue
}

func (w *taskWizard) resolvedFlags() db.TaskFlags {
	var flags db.TaskFlags
	for _, choice := range w.flagValues {
		switch choice {
		case "skip_perms":
			flags.DangerouslySkipPermissions = true
		case "debug":
			flags.Debug = true
		}
	}
	return flags
}

func (w *taskWizard) resolvedSelectedRepos() []string {
	if w.repoPicker != nil {
		return w.repoPicker.local.selectedRepos()
	}
	if w.reposForm != nil && w.reposValue != "" {
		return []string{w.reposValue}
	}
	return nil
}

// buildSubmitMsg collects all wizard state into a submit message.
func (w *taskWizard) buildSubmitMsg() tea.Msg {
	if w.editMode {
		return wizardEditSubmitMsg{
			TaskID:         w.editTaskID,
			Flags:          w.resolvedFlags(),
			SandboxProfile: w.resolvedSandboxProfile(),
			SelectedRepos:  w.resolvedSelectedRepos(),
		}
	}

	result := wizardSubmitMsg{
		Name:           w.nameValue,
		Flags:          w.resolvedFlags(),
		SandboxProfile: w.resolvedSandboxProfile(),
		SelectedRepos:  w.resolvedSelectedRepos(),
	}

	// Resolve CWD.
	if w.cwdForm != nil {
		if w.cwdValue == "." {
			result.Cwd = w.baseDir
		} else {
			result.Cwd = filepath.Join(w.baseDir, w.cwdValue)
		}
	} else if w.repoPicker == nil && w.reposForm == nil {
		result.Cwd = w.baseDir
	}

	return result
}

// View renders the wizard as a modal.
func (w *taskWizard) View(width int) string {
	w.width = width
	var b strings.Builder

	b.WriteString(w.renderTabBar())
	b.WriteString("\n\n")

	switch w.activeTab {
	case wizardTabName:
		b.WriteString(w.nameForm.View())
	case wizardTabRepos:
		if w.repoPicker != nil {
			b.WriteString(w.repoPicker.viewEmbedded())
		} else if w.reposForm != nil {
			b.WriteString(w.reposForm.View())
		} else if w.cwdForm != nil {
			b.WriteString(w.cwdForm.View())
		}
	case wizardTabOptions:
		b.WriteString(w.optionsForm.View())
	}

	// The repo picker's viewEmbedded() already includes trailing
	// whitespace; huh forms don't, so add an extra blank line.
	if w.activeTab == wizardTabRepos && w.repoPicker != nil {
		b.WriteString("\n")
	} else {
		b.WriteString("\n\n")
	}
	b.WriteString(w.renderHints())

	return b.String()
}

func (w *taskWizard) renderTabBar() string {
	activeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(w.theme.Title).
		Background(w.theme.Surface).
		Padding(0, 1)
	inactiveStyle := lipgloss.NewStyle().
		Foreground(w.theme.Muted).
		Background(w.theme.Surface).
		Padding(0, 1)
	barBgStyle := lipgloss.NewStyle().
		Background(w.theme.Surface)

	tabLabelMap := map[wizardTab]string{
		wizardTabName:    "Name",
		wizardTabRepos:   "Repos",
		wizardTabOptions: "Flags",
	}
	if !w.editMode && w.repoSets == nil {
		tabLabelMap[wizardTabRepos] = "Directory"
	}

	var tabLabels []string
	for _, tab := range w.tabOrder() {
		tabLabels = append(tabLabels, tabLabelMap[tab])
	}

	var parts []string
	for i, label := range tabLabels {
		styled := fmt.Sprintf(" %s ", label)
		logicalTab := w.logicalTab(i)
		if logicalTab == w.activeTab {
			parts = append(parts, activeStyle.Render(styled))
		} else {
			parts = append(parts, inactiveStyle.Render(styled))
		}
	}

	tabsContent := strings.Join(parts, barBgStyle.Render(" "))

	// Navigation hint on the right side.
	hintStyle := lipgloss.NewStyle().
		Foreground(w.theme.Muted).
		Background(w.theme.Surface)
	keyHintStyle := lipgloss.NewStyle().
		Foreground(w.theme.Accent).
		Background(w.theme.Surface)
	navHint := keyHintStyle.Render("[") + hintStyle.Render("/") + keyHintStyle.Render("]") + hintStyle.Render(" tabs")

	// Calculate padding between tabs and hint.
	innerWidth := w.width - 6 // account for modal padding + border
	tabsWidth := lipgloss.Width(tabsContent)
	hintWidth := lipgloss.Width(navHint)
	gap := innerWidth - tabsWidth - hintWidth
	if gap < 2 {
		gap = 2
	}

	return barBgStyle.Render(tabsContent + strings.Repeat(" ", gap) + navHint)
}

// logicalTab maps a visual tab index to a wizardTab.
func (w *taskWizard) logicalTab(visualIndex int) wizardTab {
	tabs := w.tabOrder()
	if visualIndex < len(tabs) {
		return tabs[visualIndex]
	}
	return wizardTabName
}

func (w *taskWizard) renderHints() string {
	hints := []string{}

	switch w.activeTab {
	case wizardTabName:
		hints = append(hints,
			renderPickerHint(w.theme, "enter", "next"),
			renderPickerHint(w.theme, "esc", "cancel"),
		)
	case wizardTabRepos:
		if w.repoPicker != nil {
			hints = append(hints,
				renderPickerHint(w.theme, "tab", "local/remote"),
				renderPickerHint(w.theme, "space", "toggle"),
				renderPickerHint(w.theme, "/", "filter"),
				renderPickerHint(w.theme, "enter", "create"),
				renderPickerHint(w.theme, "esc", "cancel"),
			)
		} else {
			hints = append(hints,
				renderPickerHint(w.theme, "enter", "create"),
				renderPickerHint(w.theme, "esc", "cancel"),
			)
		}
	case wizardTabOptions:
		hints = append(hints,
			renderPickerHint(w.theme, "tab/shift+tab", "fields"),
			renderPickerHint(w.theme, "enter", "create"),
			renderPickerHint(w.theme, "esc", "cancel"),
		)
	}

	return strings.Join(hints, "  ")
}

// isTextInputActive returns true if the repo picker has a text input focused.
func (tp *tabbedRepoPicker) isTextInputActive() bool {
	if tp.activeTab == pickerTabLocal && tp.local.filtering {
		return true
	}
	if tp.activeTab == pickerTabRemote {
		switch tp.remote.phase {
		case remotePhaseOrgEntry, remotePhaseSearch:
			return true
		}
	}
	return false
}
