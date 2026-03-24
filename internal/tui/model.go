package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/huh"
	"github.com/dpetersen/krang/internal/config"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/hooks"
	"github.com/dpetersen/krang/internal/summary"
	"github.com/dpetersen/krang/internal/task"
	"github.com/dpetersen/krang/internal/tmux"
	"github.com/dpetersen/krang/internal/workspace"
)

type Model struct {
	manager    *task.Manager
	taskStore  *db.TaskStore
	eventStore *db.EventStore
	hookEvents      <-chan hooks.HookEvent
	summaryPipeline *summary.Pipeline
	activeSession   string
	parkedSession   string
	cfg             config.Config
	styles          Styles
	tasks           []db.Task
	cursor     int
	sortByPriority bool
	mode       InputMode
	width      int
	height     int

	filterInput textinput.Model
	filterText  string

	sitRepViewport viewport.Model
	sitRepContent  string

	helpViewport viewport.Model

	pendingFlags   db.TaskFlags
	flagEditTaskID string

	repoSets *workspace.RepoSets

	activeForm              *huh.Form
	taskCreationResult      *taskCreationResult
	workspaceTaskResult     *workspaceTaskResult
	importFormResult        *importResult
	flagEditFormResult      *flagEditResult
	activeRepoPicker        *repoPicker
	addReposTaskID          string
	addReposWorkspaceDir    string

	workspaceProgressLines []string

	debugLog []string

	windowStylesSynced bool
}

type flagDefinition struct {
	Label             string
	Description       string
	Get               func(db.TaskFlags) bool
	Set               func(*db.TaskFlags, bool)
	RequiresRelaunch  bool
}

var flagDefinitions = []flagDefinition{
	{
		Label:            "No Sandbox",
		Description:      "Launch claude directly (skip sandbox wrapper)",
		Get:              func(f db.TaskFlags) bool { return f.NoSandbox },
		Set:              func(f *db.TaskFlags, v bool) { f.NoSandbox = v },
		RequiresRelaunch: true,
	},
	{
		Label:            "Skip Permissions",
		Description:      "Pass --dangerously-skip-permissions",
		Get:              func(f db.TaskFlags) bool { return f.DangerouslySkipPermissions },
		Set:              func(f *db.TaskFlags, v bool) { f.DangerouslySkipPermissions = v },
		RequiresRelaunch: true,
	},
	{
		Label:            "Debug",
		Description:      "Export KRANG_DEBUG=1 for hook relay logging",
		Get:              func(f db.TaskFlags) bool { return f.Debug },
		Set:              func(f *db.TaskFlags, v bool) { f.Debug = v },
		RequiresRelaunch: true,
	},
}

func NewModel(manager *task.Manager, taskStore *db.TaskStore, eventStore *db.EventStore, hookEvents <-chan hooks.HookEvent, summaryPipeline *summary.Pipeline, activeSession, parkedSession string, cfg config.Config, styles Styles) Model {
	filterInput := textinput.New()
	filterInput.Placeholder = "filter tasks..."
	filterInput.CharLimit = 40

	// Try to load workspace config; nil means no workspace mode.
	cwd, _ := os.Getwd()
	rs, _ := workspace.Load(cwd)

	return Model{
		manager:         manager,
		taskStore:        taskStore,
		eventStore:       eventStore,
		hookEvents:       hookEvents,
		summaryPipeline: summaryPipeline,
		activeSession:   activeSession,
		parkedSession:   parkedSession,
		cfg:             cfg,
		styles:          styles,
		repoSets:        rs,
		filterInput:     filterInput,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.refreshTasks,
		m.reconcileTick(),
		m.waitForHookEvent(),
		m.summaryTick(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case TasksRefreshedMsg:
		m.tasks = msg.Tasks
		if m.cursor >= len(m.filteredTasks()) && len(m.filteredTasks()) > 0 {
			m.cursor = len(m.filteredTasks()) - 1
		}
		if !m.windowStylesSynced {
			m.windowStylesSynced = true
			return m, m.syncWindowStyles()
		}
		return m, nil

	case HookEventMsg:
		t, _ := m.taskStore.GetBySessionID(msg.Event.SessionID)

		// On SessionStart with unknown ID, try to adopt it for a
		// recently woken task whose old session ID no longer matches.
		if t == nil && msg.Event.HookEventName == "SessionStart" {
			t = m.tryAdoptSession(msg.Event)
		}

		if t == nil {
			return m, m.waitForHookEvent()
		}

		logLine := fmt.Sprintf("[%s] %s task=%s",
			time.Now().Format("15:04:05"),
			msg.Event.HookEventName,
			t.Name,
		)
		if msg.Event.NotificationType != "" {
			logLine += " type=" + msg.Event.NotificationType
		}
		if msg.Event.ToolName != "" {
			logLine += " tool=" + msg.Event.ToolName
		}
		m.appendDebugLog(logLine)
		return m, tea.Batch(
			m.handleHookEvent(msg.Event),
			m.waitForHookEvent(),
		)

	case SummaryTickMsg:
		return m, tea.Batch(
			m.doSummarize,
			m.summaryTick(),
		)

	case SummariesUpdatedMsg:
		for _, line := range msg.DebugLines {
			m.appendDebugLog(line)
		}
		return m, m.refreshTasks

	case SitRepResultMsg:
		if msg.Err != nil {
			m.appendDebugLog(fmt.Sprintf("[%s] ERROR: sit rep: %v",
				time.Now().Format("15:04:05"), msg.Err))
			m.mode = ModeNormal
			return m, nil
		}
		m.sitRepContent = msg.Content
		contentWidth := m.width - 2
		contentHeight := m.height - 4
		if contentWidth < 30 {
			contentWidth = 30
		}
		if contentHeight < 6 {
			contentHeight = 6
		}

		renderer, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(contentWidth),
		)
		var rendered string
		if err == nil {
			rendered, err = renderer.Render(msg.Content)
		}
		if err != nil {
			rendered = wordWrap(msg.Content, contentWidth)
		}

		m.sitRepViewport = viewport.New(contentWidth, contentHeight)
		m.sitRepViewport.SetContent(rendered)
		m.mode = ModeSitRep
		return m, nil

	case ReconcileTickMsg:
		return m, tea.Batch(
			m.doReconcile,
			m.reconcileTick(),
		)

	case ErrorMsg:
		m.appendDebugLog(fmt.Sprintf("[%s] ERROR: %v",
			time.Now().Format("15:04:05"), msg.Err))
		return m, nil

	case formCompletedMsg:
		return m.handleFormCompleted(msg)
	case formCancelledMsg:
		m.mode = ModeNormal
		m.activeForm = nil
		return m, nil

	case workspaceProgressMsg:
		m.workspaceProgressLines = msg.Lines
		if msg.Done {
			m.mode = ModeNormal
			m.workspaceProgressLines = nil
			if msg.Err != nil {
				m.appendDebugLog(fmt.Sprintf("[%s] workspace error: %v",
					time.Now().Format("15:04:05"), msg.Err))
			}
			return m, m.refreshTasks
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.mode == ModeForm && m.activeForm != nil {
		return m.handleFormUpdate(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case ModeConfirmKill:
		return m.handleConfirmKillKey(msg)
	case ModeHelp:
		return m.handleHelpKey(msg)
	case ModeSitRepLoading, ModeWorkspaceProgress:
		return m, nil
	case ModeSitRep:
		return m.handleSitRepKey(msg)
	case ModeFilter:
		return m.handleFilterKey(msg)
	case ModeForm:
		return m.handleFormUpdate(msg)
	case ModeRepoSelect:
		return m.handleRepoSelectKey(msg)
	case ModeConfirmRelaunch:
		return m.handleConfirmRelaunchKey(msg)
	default:
		return m.handleNormalKey(msg)
	}
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.filterText != "" {
			m.filterText = ""
			m.cursor = 0
		}
		return m, nil

	case "q", "ctrl+c":
		return m, tea.Quit

	case "j", "down":
		if m.cursor < len(m.filteredTasks())-1 {
			m.cursor++
		}
		return m, nil

	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "n":
		if m.repoSets != nil && m.repoSets.WorkspaceStrategy != "" {
			repos, _ := m.repoSets.ListRepos()
			if len(repos) > 0 {
				singleRepo := m.repoSets.WorkspaceStrategy == workspace.StrategySingleRepo
				form, result := newWorkspaceTaskForm(m.taskStore.NameInUse, repos, singleRepo, m.huhTheme())
				m.activeForm = form
				m.workspaceTaskResult = result
				m.mode = ModeForm
				return m, m.activeForm.Init()
			}
		}
		baseDir, err := os.Getwd()
		if err != nil {
			return m, func() tea.Msg { return ErrorMsg{Err: err} }
		}
		form, result := newTaskCreationForm(m.taskStore.NameInUse, baseDir, m.huhTheme())
		m.activeForm = form
		m.taskCreationResult = result
		m.mode = ModeForm
		return m, m.activeForm.Init()

	case "enter":
		return m, m.focusSelected()

	case "p":
		return m, m.parkSelected()

	case "u":
		return m, m.unparkSelected()

	case "f":
		return m, m.dormifySelected()

	case "t":
		return m, m.wakeSelected()

	case "x":
		if m.selectedTask() != nil {
			m.mode = ModeConfirmKill
		}
		return m, nil

	case "c":
		t := m.selectedTask()
		if t != nil && t.WorkspaceDir != "" {
			m.mode = ModeWorkspaceProgress
			m.workspaceProgressLines = []string{fmt.Sprintf("Completing %q...", t.Name)}
		}
		return m, m.completeSelected()

	case "s":
		m.sortByPriority = !m.sortByPriority
		return m, nil

	case "r":
		return m, m.doSummarize

	case "S":
		m.mode = ModeSitRepLoading
		return m, m.generateSitRep

	case "i":
		form, result := newImportForm(m.taskStore.NameInUse, m.huhTheme())
		m.activeForm = form
		m.importFormResult = result
		m.mode = ModeForm
		return m, m.activeForm.Init()

	case "F":
		t := m.selectedTask()
		if t != nil && t.State != db.StateCompleted && t.State != db.StateFailed {
			m.flagEditTaskID = t.ID
			form, result := newFlagEditForm(t.Flags, t.Name, m.huhTheme())
			m.activeForm = form
			m.flagEditFormResult = result
			m.mode = ModeForm
			return m, m.activeForm.Init()
		}
		return m, nil

	case "W":
		t := m.selectedTask()
		if t == nil || t.WorkspaceDir == "" || m.repoSets == nil {
			return m, nil
		}
		if m.repoSets.WorkspaceStrategy != workspace.StrategyMultiRepo {
			return m, nil
		}
		allRepos, _ := m.repoSets.ListRepos()
		present := workspace.PresentRepos(t.WorkspaceDir)
		presentSet := make(map[string]bool)
		for _, r := range present {
			presentSet[r] = true
		}
		var available []string
		for _, r := range allRepos {
			if !presentSet[r] {
				available = append(available, r)
			}
		}
		if len(available) == 0 {
			return m, nil
		}
		title := fmt.Sprintf("Add repos to %q:", t.Name)
		picker := newRepoPicker(title, m.repoSets.Sets, available, m.styles)
		m.activeRepoPicker = &picker
		m.addReposTaskID = t.ID
		m.addReposWorkspaceDir = t.WorkspaceDir
		m.mode = ModeRepoSelect
		return m, nil

	case "+":
		return m, m.createCompanion()

	case "C":
		return m, m.compactWindows()

	case "?":
		m.helpViewport = viewport.New(m.width-2, m.height-4)
		m.helpViewport.SetContent(buildHelpContent())
		m.mode = ModeHelp
		return m, nil

	case "/":
		m.mode = ModeFilter
		m.filterInput.Reset()
		m.filterInput.Focus()
		return m, m.filterInput.Cursor.BlinkCmd()
	}

	return m, nil
}


func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = ModeNormal
		m.filterText = ""
		m.cursor = 0
		return m, nil
	case "enter":
		m.mode = ModeNormal
		m.filterText = strings.TrimSpace(m.filterInput.Value())
		m.cursor = 0
		return m, nil
	}

	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	return m, cmd
}


func (m Model) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "?":
		m.mode = ModeNormal
		return m, nil
	}
	var cmd tea.Cmd
	m.helpViewport, cmd = m.helpViewport.Update(msg)
	return m, cmd
}

func (m Model) handleSitRepKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "S":
		m.mode = ModeNormal
		return m, nil
	}
	var cmd tea.Cmd
	m.sitRepViewport, cmd = m.sitRepViewport.Update(msg)
	return m, cmd
}

func (m Model) generateSitRep() tea.Msg {
	tasks, err := m.taskStore.List()
	if err != nil {
		return SitRepResultMsg{Err: err}
	}

	content, err := summary.GenerateSitRep(summary.SitRepInput{
		Tasks:      tasks,
		ScreenRows: m.height,
		ScreenCols: m.width,
	})
	if err != nil {
		return SitRepResultMsg{Err: err}
	}

	return SitRepResultMsg{Content: content}
}


func (m Model) handleConfirmRelaunchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		taskID := m.flagEditTaskID
		flags := m.pendingFlags
		m.mode = ModeNormal
		return m, func() tea.Msg {
			if err := m.taskStore.UpdateFlags(taskID, flags); err != nil {
				return ErrorMsg{Err: err}
			}
			if err := m.manager.Relaunch(taskID); err != nil {
				return ErrorMsg{Err: err}
			}
			return m.refreshTasks()
		}
	default:
		m.mode = ModeNormal
		return m, nil
	}
}

func (m Model) handleRepoSelectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.activeRepoPicker.moveDown()
	case "k", "up":
		m.activeRepoPicker.moveUp()
	case " ":
		m.activeRepoPicker.toggle()
	case "enter":
		selected := m.activeRepoPicker.selectedRepos()
		if len(selected) == 0 {
			return m, nil
		}

		// Add-repos flow.
		if m.addReposTaskID != "" {
			rs := m.repoSets
			workspaceDir := m.addReposWorkspaceDir
			taskName := filepath.Base(workspaceDir)
			m.activeRepoPicker = nil
			m.addReposTaskID = ""
			m.addReposWorkspaceDir = ""
			m.mode = ModeWorkspaceProgress
			m.workspaceProgressLines = []string{fmt.Sprintf("Adding repos to %q...", taskName)}
			return m, m.addReposToWorkspace(workspaceDir, taskName, selected, rs)
		}

		// New workspace task flow.
		result := m.workspaceTaskResult
		rs := m.repoSets
		m.workspaceTaskResult = nil
		m.activeRepoPicker = nil
		m.mode = ModeWorkspaceProgress
		m.workspaceProgressLines = []string{fmt.Sprintf("Creating workspace %q...", result.Name)}
		return m, m.createWorkspaceTask(result.Name, result.Flags, selected, rs)
	case "esc":
		m.workspaceTaskResult = nil
		m.activeRepoPicker = nil
		m.addReposTaskID = ""
		m.addReposWorkspaceDir = ""
		m.mode = ModeNormal
	}
	return m, nil
}

func (m Model) handleFormUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.activeForm == nil {
		m.mode = ModeNormal
		return m, nil
	}
	model, cmd := m.activeForm.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		m.activeForm = f
	}
	return m, cmd
}

func (m Model) handleFormCompleted(msg formCompletedMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeNormal
	m.activeForm = nil

	switch msg.formType {
	case formTypeNewTask:
		if m.taskCreationResult == nil {
			return m, nil
		}
		result := m.taskCreationResult
		m.taskCreationResult = nil
		return m, m.createTask(result.Name, "", result.Cwd, result.Flags)

	case formTypeWorkspaceTask:
		if m.workspaceTaskResult == nil {
			return m, nil
		}
		result := m.workspaceTaskResult
		rs := m.repoSets

		// single_repo: repos already selected in the form.
		if rs.WorkspaceStrategy == workspace.StrategySingleRepo {
			m.workspaceTaskResult = nil
			m.mode = ModeWorkspaceProgress
			m.workspaceProgressLines = []string{fmt.Sprintf("Creating workspace %q...", result.Name)}
			return m, m.createWorkspaceTask(result.Name, result.Flags, result.SelectedRepos, rs)
		}

		// multi_repo: show the repo picker.
		repos, _ := rs.ListRepos()
		title := fmt.Sprintf("Select repos for %q:", result.Name)
		picker := newRepoPicker(title, rs.Sets, repos, m.styles)
		m.activeRepoPicker = &picker
		m.mode = ModeRepoSelect
		return m, nil

	case formTypeImport:
		if m.importFormResult == nil {
			return m, nil
		}
		result := m.importFormResult
		m.importFormResult = nil
		return m, m.importTask(result.Name, result.SessionID)

	case formTypeFlagEdit:
		if m.flagEditFormResult == nil {
			return m, nil
		}
		result := m.flagEditFormResult
		taskID := m.flagEditTaskID
		m.flagEditFormResult = nil

		var originalTask *db.Task
		for i := range m.tasks {
			if m.tasks[i].ID == taskID {
				originalTask = &m.tasks[i]
				break
			}
		}
		if originalTask == nil {
			return m, nil
		}

		// For dormant tasks, just save flags directly.
		if originalTask.State == db.StateDormant {
			return m, func() tea.Msg {
				if err := m.taskStore.UpdateFlags(taskID, result.Flags); err != nil {
					return ErrorMsg{Err: err}
				}
				return m.refreshTasks()
			}
		}

		// Check if any relaunch-requiring flag changed.
		relaunchNeeded := false
		for _, fd := range flagDefinitions {
			if fd.RequiresRelaunch && fd.Get(result.Flags) != fd.Get(originalTask.Flags) {
				relaunchNeeded = true
				break
			}
		}

		if !relaunchNeeded {
			return m, func() tea.Msg {
				if err := m.taskStore.UpdateFlags(taskID, result.Flags); err != nil {
					return ErrorMsg{Err: err}
				}
				return m.refreshTasks()
			}
		}

		// Need confirmation for relaunch.
		m.pendingFlags = result.Flags
		m.mode = ModeConfirmRelaunch
		return m, nil
	}

	return m, nil
}

func (m Model) huhTheme() *huh.Theme {
	return huh.ThemeCatppuccin()
}

func (m Model) handleConfirmKillKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		t := m.selectedTask()
		if t != nil && t.WorkspaceDir != "" {
			m.mode = ModeWorkspaceProgress
			m.workspaceProgressLines = []string{fmt.Sprintf("Killing %q...", t.Name)}
		} else {
			m.mode = ModeNormal
		}
		return m, m.killSelected()
	default:
		m.mode = ModeNormal
		return m, nil
	}
}

const maxDebugLines = 20

func (m *Model) appendDebugLog(line string) {
	m.debugLog = append(m.debugLog, line)
	if len(m.debugLog) > maxDebugLines {
		m.debugLog = m.debugLog[len(m.debugLog)-maxDebugLines:]
	}
}

// tryAdoptSession matches an unknown SessionStart to an active task
// whose cwd matches the event's cwd. This handles resumed sessions
// which get a new session ID.
func (m *Model) tryAdoptSession(event hooks.HookEvent) *db.Task {
	tasks, err := m.taskStore.List()
	if err != nil {
		return nil
	}

	for _, t := range tasks {
		if t.State != db.StateActive {
			continue
		}
		if t.TmuxWindow == "" {
			continue
		}
		if t.Cwd == event.Cwd {
			_ = m.taskStore.UpdateSessionID(t.ID, event.SessionID)
			m.appendDebugLog(fmt.Sprintf("[%s] adopted session for task=%s",
				time.Now().Format("15:04:05"), t.Name))
			updated := t
			updated.SessionID = event.SessionID
			return &updated
		}
	}
	return nil
}

func (m Model) filteredTasks() []db.Task {
	var tasks []db.Task

	if m.sortByPriority {
		// Priority mode: active only, sorted by attention urgency.
		for _, t := range m.tasks {
			if t.State == db.StateActive {
				tasks = append(tasks, t)
			}
		}
		sort.SliceStable(tasks, func(i, j int) bool {
			return attentionPriority(tasks[i].Attention) < attentionPriority(tasks[j].Attention)
		})
	} else {
		tasks = m.tasks
	}

	if m.filterText == "" {
		return tasks
	}
	filter := strings.ToLower(m.filterText)
	var filtered []db.Task
	for _, t := range tasks {
		if strings.Contains(strings.ToLower(t.Name), filter) ||
			strings.Contains(strings.ToLower(string(t.State)), filter) ||
			strings.Contains(strings.ToLower(t.Summary), filter) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func attentionPriority(a db.AttentionState) int {
	switch a {
	case db.AttentionPermission:
		return 0
	case db.AttentionError:
		return 1
	case db.AttentionWaiting:
		return 2
	case db.AttentionOK:
		return 3
	case db.AttentionDone:
		return 4
	default:
		return 5
	}
}

func (m Model) selectedTask() *db.Task {
	tasks := m.filteredTasks()
	if m.cursor < 0 || m.cursor >= len(tasks) {
		return nil
	}
	return &tasks[m.cursor]
}

func (m Model) refreshTasks() tea.Msg {
	tasks, err := m.manager.ListTasks()
	if err != nil {
		return ErrorMsg{Err: err}
	}
	return TasksRefreshedMsg{Tasks: tasks}
}

func (m Model) reconcileTick() tea.Cmd {
	return tea.Tick(10*time.Second, func(time.Time) tea.Msg {
		return ReconcileTickMsg{}
	})
}

func (m Model) doReconcile() tea.Msg {
	if err := m.manager.Reconcile(); err != nil {
		return ErrorMsg{Err: err}
	}
	return m.refreshTasks()
}

func (m Model) summaryTick() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg {
		return SummaryTickMsg{}
	})
}

func (m Model) doSummarize() tea.Msg {
	now := time.Now().Format("15:04:05")
	var debugLines []string

	if m.summaryPipeline == nil {
		debugLines = append(debugLines, fmt.Sprintf("[%s] summary: pipeline is nil", now))
		return SummariesUpdatedMsg{DebugLines: debugLines}
	}

	tasks, err := m.taskStore.List()
	if err != nil {
		debugLines = append(debugLines, fmt.Sprintf("[%s] summary: list error: %v", now, err))
		return SummariesUpdatedMsg{DebugLines: debugLines}
	}

	eligible := 0
	for _, t := range tasks {
		if t.TmuxWindow != "" && (t.State == db.StateActive || t.State == db.StateParked) {
			eligible++
		}
	}
	debugLines = append(debugLines, fmt.Sprintf("[%s] summary: running for %d eligible tasks", now, eligible))

	results := m.summaryPipeline.SummarizeAll(tasks)
	for _, r := range results {
		debugLines = append(debugLines, fmt.Sprintf("[%s] summary: %s", now, r))
	}

	return SummariesUpdatedMsg{DebugLines: debugLines}
}

func (m Model) createTask(name, prompt, cwd string, flags db.TaskFlags) tea.Cmd {
	return func() tea.Msg {
		if _, err := m.manager.CreateTask(name, prompt, cwd, flags); err != nil {
			return ErrorMsg{Err: err}
		}
		return m.refreshTasks()
	}
}

func (m Model) createWorkspaceTask(name string, flags db.TaskFlags, repos []string, rs *workspace.RepoSets) tea.Cmd {
	return func() tea.Msg {
		lines := []string{fmt.Sprintf("Creating workspace %q...", name)}

		for _, repo := range repos {
			vcs := rs.DetectVCS(repo)
			lines = append(lines, fmt.Sprintf("  Cloning %s (%s)...", repo, vcs))
		}

		result, err := workspace.Create(rs, name, repos)
		if err != nil {
			return workspaceProgressMsg{
				Lines: append(lines, fmt.Sprintf("  Error: %v", err)),
				Done:  true,
				Err:   err,
			}
		}

		for repo, vcs := range result.Created {
			lines = append(lines, fmt.Sprintf("  Done: %s (%s)", repo, vcs))
		}
		for _, e := range result.Errors {
			lines = append(lines, fmt.Sprintf("  Failed: %s", e))
		}

		lines = append(lines, "Launching Claude...")

		t, err := m.manager.CreateTask(name, "", result.WorkspaceDir, flags)
		if err != nil {
			return workspaceProgressMsg{
				Lines: append(lines, fmt.Sprintf("  Error: %v", err)),
				Done:  true,
				Err:   err,
			}
		}

		if err := m.taskStore.UpdateWorkspaceDir(t.ID, result.WorkspaceDir); err != nil {
			return workspaceProgressMsg{
				Lines: append(lines, fmt.Sprintf("  Error: %v", err)),
				Done:  true,
				Err:   err,
			}
		}

		return workspaceProgressMsg{Lines: lines, Done: true}
	}
}

func (m Model) addReposToWorkspace(workspaceDir, taskName string, repos []string, rs *workspace.RepoSets) tea.Cmd {
	return func() tea.Msg {
		lines := []string{fmt.Sprintf("Adding repos to %q...", taskName)}

		result, err := workspace.AddRepos(rs, workspaceDir, taskName, repos)
		if err != nil {
			lines = append(lines, fmt.Sprintf("  Error: %v", err))
			return workspaceProgressMsg{Lines: lines, Done: true, Err: err}
		}

		for repo, vcs := range result.Created {
			lines = append(lines, fmt.Sprintf("  Added: %s (%s)", repo, vcs))
		}
		for _, e := range result.Errors {
			lines = append(lines, fmt.Sprintf("  Failed: %s", e))
		}

		return workspaceProgressMsg{Lines: lines, Done: true}
	}
}

func (m Model) importTask(name, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if err := m.manager.ImportTask(name, sessionID); err != nil {
			return ErrorMsg{Err: err}
		}
		return m.refreshTasks()
	}
}

func (m Model) focusSelected() tea.Cmd {
	t := m.selectedTask()
	if t == nil || t.TmuxWindow == "" {
		return nil
	}
	return func() tea.Msg {
		if err := m.manager.Focus(t.ID); err != nil {
			return ErrorMsg{Err: err}
		}
		return nil
	}
}

func (m Model) parkSelected() tea.Cmd {
	t := m.selectedTask()
	if t == nil {
		return nil
	}
	return func() tea.Msg {
		if err := m.manager.Park(t.ID); err != nil {
			return ErrorMsg{Err: err}
		}
		return m.refreshTasks()
	}
}

func (m Model) unparkSelected() tea.Cmd {
	t := m.selectedTask()
	if t == nil {
		return nil
	}
	return func() tea.Msg {
		if err := m.manager.Unpark(t.ID); err != nil {
			return ErrorMsg{Err: err}
		}
		return m.refreshTasks()
	}
}

func (m Model) dormifySelected() tea.Cmd {
	t := m.selectedTask()
	if t == nil {
		return nil
	}
	return func() tea.Msg {
		if err := m.manager.Dormify(t.ID); err != nil {
			return ErrorMsg{Err: err}
		}
		return m.refreshTasks()
	}
}

func (m Model) wakeSelected() tea.Cmd {
	t := m.selectedTask()
	if t == nil {
		return nil
	}
	return func() tea.Msg {
		if err := m.manager.Wake(t.ID); err != nil {
			return ErrorMsg{Err: err}
		}
		return m.refreshTasks()
	}
}

func (m Model) killSelected() tea.Cmd {
	t := m.selectedTask()
	if t == nil {
		return nil
	}
	taskID := t.ID
	workspaceDir := t.WorkspaceDir
	rs := m.repoSets
	return func() tea.Msg {
		if err := m.manager.Kill(taskID); err != nil {
			return ErrorMsg{Err: err}
		}
		if workspaceDir != "" {
			return destroyWorkspace(rs, workspaceDir)
		}
		return m.refreshTasks()
	}
}

func (m Model) waitForHookEvent() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.hookEvents
		if !ok {
			return nil
		}
		return HookEventMsg{Event: event}
	}
}

func (m Model) handleHookEvent(event hooks.HookEvent) tea.Cmd {
	return func() tea.Msg {
		// Task lookup already done in Update — safe to look up again for the DB writes.
		t, err := m.taskStore.GetBySessionID(event.SessionID)
		if err != nil || t == nil {
			return m.refreshTasks()
		}

		_ = m.eventStore.Log(t.ID, event.HookEventName, event.RawPayload)

		attention, ok := hooks.AttentionFromEvent(event)
		if ok {
			_ = m.taskStore.UpdateAttention(t.ID, attention)
			if t.TmuxWindow != "" {
				applyWindowStyle(t.TmuxWindow, attention, m.cfg)
			}
		}

		if event.Cwd != "" && event.Cwd != t.Cwd {
			_ = m.taskStore.UpdateCwd(t.ID, event.Cwd)
		}

		if event.TranscriptPath != "" && event.TranscriptPath != t.TranscriptPath {
			_ = m.taskStore.UpdateTranscriptPath(t.ID, event.TranscriptPath)
		}

		if event.HookEventName == "TaskCompleted" {
			_ = m.taskStore.UpdateState(t.ID, db.StateCompleted)
		}

		return m.refreshTasks()
	}
}

func applyWindowStyle(windowID string, attention db.AttentionState, cfg config.Config) {
	color := cfg.WindowColor(string(attention))
	if color != "" {
		_ = tmux.SetWindowStyle(windowID, color)
	} else {
		_ = tmux.ClearWindowStyle(windowID)
	}
}

func (m Model) syncWindowStyles() tea.Cmd {
	tasks := m.tasks
	cfg := m.cfg
	return func() tea.Msg {
		for _, t := range tasks {
			if t.TmuxWindow != "" {
				applyWindowStyle(t.TmuxWindow, t.Attention, cfg)
			}
		}
		return nil
	}
}

func (m Model) createCompanion() tea.Cmd {
	t := m.selectedTask()
	if t == nil || t.TmuxWindow == "" {
		return nil
	}
	taskName := t.Name
	windowID := t.TmuxWindow
	cwd := t.Cwd
	return func() tea.Msg {
		companionName := tmux.CompanionWindowName(taskName)
		if _, err := tmux.CreateWindowAfter(windowID, companionName, cwd); err != nil {
			return ErrorMsg{Err: err}
		}
		return m.refreshTasks()
	}
}

func (m Model) compactWindows() tea.Cmd {
	session := m.activeSession
	return func() tea.Msg {
		if err := tmux.CompactWindows(session); err != nil {
			return ErrorMsg{Err: err}
		}
		return nil
	}
}

func (m Model) completeSelected() tea.Cmd {
	t := m.selectedTask()
	if t == nil {
		return nil
	}
	taskID := t.ID
	workspaceDir := t.WorkspaceDir
	rs := m.repoSets
	return func() tea.Msg {
		if err := m.manager.Complete(taskID); err != nil {
			return ErrorMsg{Err: err}
		}
		if workspaceDir != "" {
			return destroyWorkspace(rs, workspaceDir)
		}
		return m.refreshTasks()
	}
}

func destroyWorkspace(rs *workspace.RepoSets, workspaceDir string) tea.Msg {
	lines := []string{fmt.Sprintf("Destroying workspace %s...", filepath.Base(workspaceDir))}

	err := workspace.Destroy(rs, workspaceDir)
	if err != nil {
		lines = append(lines, fmt.Sprintf("  Warning: %v", err))
	} else {
		lines = append(lines, "  Done.")
	}

	return workspaceProgressMsg{Lines: lines, Done: true, Err: err}
}
