package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/task"
)

type Model struct {
	manager *task.Manager
	tasks   []db.Task
	cursor  int
	mode    InputMode
	width   int
	height  int

	nameInput   textinput.Model
	promptInput textinput.Model

	lastError    string
	errorExpires time.Time

	pendingNewName string
}

func NewModel(manager *task.Manager) Model {
	nameInput := textinput.New()
	nameInput.Placeholder = "task-name"
	nameInput.CharLimit = 40

	promptInput := textinput.New()
	promptInput.Placeholder = "prompt for Claude (optional, Enter to skip)"
	promptInput.CharLimit = 500

	return Model{
		manager:     manager,
		nameInput:   nameInput,
		promptInput: promptInput,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.refreshTasks,
		m.reconcileTick(),
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

	case ReconcileTickMsg:
		return m, tea.Batch(
			m.doReconcile,
			m.reconcileTick(),
		)

	case ErrorMsg:
		m.lastError = msg.Err.Error()
		m.errorExpires = time.Now().Add(5 * time.Second)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.mode == ModeNewName {
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}
	if m.mode == ModeNewPrompt {
		var cmd tea.Cmd
		m.promptInput, cmd = m.promptInput.Update(msg)
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
	default:
		return m.handleNormalKey(msg)
	}
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "j", "down":
		if m.cursor < len(m.tasks)-1 {
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

	case "d":
		return m, m.dormifySelected()

	case "w":
		return m, m.wakeSelected()

	case "x":
		if m.selectedTask() != nil {
			m.mode = ModeConfirmKill
		}
		return m, nil

	case "c":
		return m, m.completeSelected()
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

func (m Model) selectedTask() *db.Task {
	if m.cursor < 0 || m.cursor >= len(m.tasks) {
		return nil
	}
	return &m.tasks[m.cursor]
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
