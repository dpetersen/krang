package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/hooks"
	"github.com/dpetersen/krang/internal/summary"
	"github.com/dpetersen/krang/internal/task"
)

type Model struct {
	manager    *task.Manager
	taskStore  *db.TaskStore
	eventStore *db.EventStore
	hookEvents      <-chan hooks.HookEvent
	summaryPipeline *summary.Pipeline
	activeSession   string
	tasks           []db.Task
	cursor     int
	sortByPriority bool
	mode       InputMode
	width      int
	height     int

	nameInput      textinput.Model
	promptInput    textinput.Model
	filterInput    textinput.Model
	sessionIDInput textinput.Model

	pendingNewName    string
	pendingImportName string
	filterText        string

	debugLog []string
}

func NewModel(manager *task.Manager, taskStore *db.TaskStore, eventStore *db.EventStore, hookEvents <-chan hooks.HookEvent, summaryPipeline *summary.Pipeline, activeSession string) Model {
	nameInput := textinput.New()
	nameInput.Placeholder = "task-name"
	nameInput.CharLimit = 40

	promptInput := textinput.New()
	promptInput.Placeholder = "prompt for Claude (optional, Enter to skip)"
	promptInput.CharLimit = 500

	filterInput := textinput.New()
	filterInput.Placeholder = "filter tasks..."
	filterInput.CharLimit = 40

	sessionIDInput := textinput.New()
	sessionIDInput.Placeholder = "Claude session ID (UUID)"
	sessionIDInput.CharLimit = 80

	return Model{
		manager:         manager,
		taskStore:        taskStore,
		eventStore:       eventStore,
		hookEvents:       hookEvents,
		summaryPipeline: summaryPipeline,
		activeSession:   activeSession,
		nameInput:      nameInput,
		promptInput:    promptInput,
		filterInput:    filterInput,
		sessionIDInput: sessionIDInput,
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
		if m.cursor >= len(m.tasks) && len(m.tasks) > 0 {
			m.cursor = len(m.tasks) - 1
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

	case ReconcileTickMsg:
		return m, tea.Batch(
			m.doReconcile,
			m.reconcileTick(),
		)

	case ErrorMsg:
		m.appendDebugLog(fmt.Sprintf("[%s] ERROR: %v",
			time.Now().Format("15:04:05"), msg.Err))
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.mode == ModeNewName || m.mode == ModeImportName {
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}
	if m.mode == ModeNewPrompt {
		var cmd tea.Cmd
		m.promptInput, cmd = m.promptInput.Update(msg)
		return m, cmd
	}
	if m.mode == ModeImportSessionID {
		var cmd tea.Cmd
		m.sessionIDInput, cmd = m.sessionIDInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case ModeNewName:
		return m.handleNewNameKey(msg)
	case ModeNewPrompt:
		return m.handleNewPromptKey(msg)
	case ModeConfirmKill:
		return m.handleConfirmKillKey(msg)
	case ModeHelp:
		m.mode = ModeNormal
		return m, nil
	case ModeFilter:
		return m.handleFilterKey(msg)
	case ModeImportName:
		return m.handleImportNameKey(msg)
	case ModeImportSessionID:
		return m.handleImportSessionIDKey(msg)
		return m.handleFilterKey(msg)
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
		m.mode = ModeNewName
		m.nameInput.Reset()
		m.nameInput.Focus()
		return m, m.nameInput.Cursor.BlinkCmd()

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
		return m, m.completeSelected()

	case "s":
		m.sortByPriority = !m.sortByPriority
		return m, nil

	case "r":
		return m, m.doSummarize

	case "i":
		m.mode = ModeImportName
		m.nameInput.Reset()
		m.nameInput.Focus()
		return m, m.nameInput.Cursor.BlinkCmd()

	case "?":
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

func (m Model) handleNewNameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = ModeNormal
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			m.mode = ModeNormal
			return m, nil
		}
		if m.taskStore.NameInUse(name) {
			m.appendDebugLog(fmt.Sprintf("[%s] ERROR: name %q already in use",
				time.Now().Format("15:04:05"), name))
			return m, nil
		}
		m.pendingNewName = name
		m.mode = ModeNewPrompt
		m.promptInput.Reset()
		m.promptInput.Focus()
		return m, m.promptInput.Cursor.BlinkCmd()
	}

	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

func (m Model) handleNewPromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = ModeNormal
		return m, nil
	case "enter":
		prompt := strings.TrimSpace(m.promptInput.Value())
		name := m.pendingNewName
		m.mode = ModeNormal
		m.pendingNewName = ""
		return m, m.createTask(name, prompt)
	}

	var cmd tea.Cmd
	m.promptInput, cmd = m.promptInput.Update(msg)
	return m, cmd
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

func (m Model) handleImportNameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = ModeNormal
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			m.mode = ModeNormal
			return m, nil
		}
		if m.taskStore.NameInUse(name) {
			m.appendDebugLog(fmt.Sprintf("[%s] ERROR: name %q already in use",
				time.Now().Format("15:04:05"), name))
			return m, nil
		}
		m.pendingImportName = name
		m.mode = ModeImportSessionID
		m.sessionIDInput.Reset()
		m.sessionIDInput.Focus()
		return m, m.sessionIDInput.Cursor.BlinkCmd()
	}

	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

func (m Model) handleImportSessionIDKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = ModeNormal
		return m, nil
	case "enter":
		sessionID := strings.TrimSpace(m.sessionIDInput.Value())
		if sessionID == "" {
			m.mode = ModeNormal
			return m, nil
		}
		name := m.pendingImportName
		m.mode = ModeNormal
		m.pendingImportName = ""
		return m, m.importTask(name, sessionID)
	}

	var cmd tea.Cmd
	m.sessionIDInput, cmd = m.sessionIDInput.Update(msg)
	return m, cmd
}

func (m Model) handleConfirmKillKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.mode = ModeNormal
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

func (m Model) createTask(name, prompt string) tea.Cmd {
	return func() tea.Msg {
		cwd, err := os.Getwd()
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("getting cwd: %w", err)}
		}
		if _, err := m.manager.CreateTask(name, prompt, cwd); err != nil {
			return ErrorMsg{Err: err}
		}
		return m.refreshTasks()
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
	return func() tea.Msg {
		if err := m.manager.Kill(t.ID); err != nil {
			return ErrorMsg{Err: err}
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
		}

		if event.Cwd != "" && event.Cwd != t.Cwd {
			_ = m.taskStore.UpdateCwd(t.ID, event.Cwd)
		}

		if event.HookEventName == "TaskCompleted" {
			_ = m.taskStore.UpdateState(t.ID, db.StateCompleted)
		}

		return m.refreshTasks()
	}
}

func (m Model) completeSelected() tea.Cmd {
	t := m.selectedTask()
	if t == nil {
		return nil
	}
	return func() tea.Msg {
		if err := m.manager.Complete(t.ID); err != nil {
			return ErrorMsg{Err: err}
		}
		return m.refreshTasks()
	}
}
