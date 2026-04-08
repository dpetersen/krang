package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/huh"
	"github.com/dpetersen/krang/internal/classify"
	"github.com/dpetersen/krang/internal/config"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/github"
	"github.com/dpetersen/krang/internal/hooks"
	"github.com/dpetersen/krang/internal/proctree"
	"github.com/dpetersen/krang/internal/summary"
	"github.com/dpetersen/krang/internal/task"
	"github.com/dpetersen/krang/internal/tmux"
	"github.com/dpetersen/krang/internal/usage"
	"github.com/dpetersen/krang/internal/workspace"
)

type Model struct {
	manager         *task.Manager
	taskStore       *db.TaskStore
	eventStore      *db.EventStore
	hookEvents      <-chan hooks.HookEvent
	summaryPipeline *summary.Pipeline
	activeSession   string
	parkedSession   string
	cfg             config.Config
	styles          Styles
	tasks           []db.Task
	cursor          int
	sortByPriority  bool
	mode            InputMode
	width           int
	height          int

	filterInput textinput.Model
	filterText  string

	sitRepViewport viewport.Model
	sitRepContent  string

	helpViewport viewport.Model

	pendingFlags          db.TaskFlags
	pendingSandboxProfile string
	flagEditTaskID        string

	repoSets *workspace.RepoSets

	activeForm            *huh.Form
	importFormResult      *importResult
	activeWizard          *taskWizard
	activeRepoPicker      *tabbedRepoPicker
	remoteSearchGen       uint64
	returnToPickerOnClone bool
	addReposTaskID        string
	addReposWorkspaceDir  string

	workspaceProgressLines []string
	wsProgress             *wsProgressState

	debugLog []string

	taskProcesses map[string]*proctree.TaskProcesses
	subagents     map[string]map[string]string // taskID → agentID → agentType
	pendingPerms  map[string]map[string]bool   // taskID → agent_ids with unresolved permissions

	pendingOps    map[string]string // taskID → operation label (e.g. "freezing...")
	classifyGen   map[string]uint64 // taskID → generation counter for cancellation
	spinner       spinner.Model
	windowIndexes map[string]string // tmux window ID → display index

	windowStylesSynced bool

	sparklineWindow SparklineWindow
	sparklineData   map[string][]sparklineBucket

	usageCache   map[string]*usage.UsageSummary
	usageLoading map[string]bool

	paletteCursor int

	// Fork dialog state.
	forkNameInput  textinput.Model
	forkMode       int      // 0=independent, 1=shared
	forkSourceTask *db.Task // task being forked

	// Contested sessions: source session ID → fork workspace cwd.
	// Set BEFORE the fork launches so early events from the forked
	// Claude (which use the source session ID) don't corrupt the
	// original task's state.
	contestedSessions map[string]string

	// Set when entering ModeConfirmComplete for a workspace task.
	// Each entry is a repo name (multi_repo) or empty for single_repo.
	confirmUncommittedRepos []string
	confirmUnpushedRepos    []string
}

func NewModel(manager *task.Manager, taskStore *db.TaskStore, eventStore *db.EventStore, hookEvents <-chan hooks.HookEvent, summaryPipeline *summary.Pipeline, activeSession, parkedSession string, cfg config.Config, styles Styles) Model {
	filterInput := textinput.New()
	filterInput.Placeholder = "filter tasks..."
	filterInput.CharLimit = 40

	// Try to load workspace config; nil means no workspace mode.
	cwd, _ := os.Getwd()
	rs, _ := workspace.Load(cwd)

	// User-level config fills in when workspace config doesn't set values.
	if rs != nil {
		if rs.DefaultVCS == "" && cfg.DefaultVCS != "" {
			rs.DefaultVCS = cfg.DefaultVCS
		}
		if len(cfg.GitHubOrgs) > 0 {
			seen := make(map[string]bool)
			for _, o := range rs.GitHubOrgs {
				seen[o] = true
			}
			for _, o := range cfg.GitHubOrgs {
				if !seen[o] {
					rs.GitHubOrgs = append(rs.GitHubOrgs, o)
				}
			}
		}
	}

	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))

	return Model{
		manager:           manager,
		taskStore:         taskStore,
		eventStore:        eventStore,
		hookEvents:        hookEvents,
		summaryPipeline:   summaryPipeline,
		activeSession:     activeSession,
		parkedSession:     parkedSession,
		cfg:               cfg,
		styles:            styles,
		repoSets:          rs,
		filterInput:       filterInput,
		pendingOps:        make(map[string]string),
		subagents:         make(map[string]map[string]string),
		pendingPerms:      make(map[string]map[string]bool),
		classifyGen:       make(map[string]uint64),
		sparklineData:     make(map[string][]sparklineBucket),
		usageCache:        make(map[string]*usage.UsageSummary),
		usageLoading:      make(map[string]bool),
		contestedSessions: make(map[string]string),
		spinner:           s,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.refreshTasks,
		m.reconcileTick(),
		m.waitForHookEvent(),
		m.summaryTick(),
		m.processTick(),
		m.sparklineTick(),
		m.fetchSparklineData,
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
		m.windowIndexes = msg.WindowIndexes
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

		// When a fork is pending, the forked Claude sends events with
		// the source session ID before SessionStart assigns a new one.
		// If this session is contested and the event cwd matches the
		// fork's workspace, route the event to the fork task instead.
		if t != nil {
			if forkCwd, contested := m.contestedSessions[msg.Event.SessionID]; contested {
				if msg.Event.Cwd != "" && msg.Event.Cwd != t.Cwd {
					// This event is from the forked Claude. Find the fork
					// task by matching cwd and empty session ID.
					tasks, _ := m.taskStore.List()
					for _, ft := range tasks {
						if ft.SessionID == "" && ft.State == db.StateActive && ft.Cwd == forkCwd {
							t = &ft
							break
						}
					}
				}
			}
		}

		// Try to adopt unknown session IDs. This handles resumed
		// sessions (new ID on SessionStart) and forked sessions
		// (Claude assigns a new ID after --fork-session that may
		// first appear on any event type, not just SessionStart).
		if t == nil {
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
		if msg.Event.AgentID != "" {
			logLine += " agent=" + msg.Event.AgentID
		}
		m.appendDebugLog(logLine)

		// Track subagent lifecycle.
		switch msg.Event.HookEventName {
		case "SubagentStart":
			if m.subagents[t.ID] == nil {
				m.subagents[t.ID] = make(map[string]string)
			}
			m.subagents[t.ID][msg.Event.AgentID] = msg.Event.AgentType
		case "SubagentStop":
			delete(m.subagents[t.ID], msg.Event.AgentID)
			if len(m.subagents[t.ID]) == 0 {
				delete(m.subagents, t.ID)
			}
			delete(m.pendingPerms[t.ID], msg.Event.AgentID)
		case "Stop", "SessionEnd":
			// Main agent stopped — all subagents are gone.
			delete(m.subagents, t.ID)
			delete(m.pendingPerms, t.ID)
		}

		// Track per-agent pending permissions so that PostToolUse
		// from a different agent doesn't clobber the permission state.
		agentKey := msg.Event.AgentID // empty string for parent agent
		switch msg.Event.HookEventName {
		case "PermissionRequest":
			if m.pendingPerms[t.ID] == nil {
				m.pendingPerms[t.ID] = make(map[string]bool)
			}
			m.pendingPerms[t.ID][agentKey] = true
		case "PostToolUse", "PostToolUseFailure":
			delete(m.pendingPerms[t.ID], agentKey)
		case "UserPromptSubmit":
			// User typed something — clear all pending permissions.
			// This is the escape hatch for denied permissions that
			// never produce a PostToolUse.
			delete(m.pendingPerms, t.ID)
		}
		suppressAttention := len(m.pendingPerms[t.ID]) > 0

		// Bump generation to invalidate any in-flight classification.
		m.classifyGen[t.ID]++
		delete(m.pendingOps, t.ID)

		classifying := msg.Event.HookEventName == "Stop" &&
			m.cfg.ClassifyAttentionEnabled() &&
			msg.Event.LastAssistantMessage != ""

		cmds := []tea.Cmd{
			m.handleHookEvent(msg.Event, classifying, suppressAttention, t.ID),
			m.waitForHookEvent(),
		}
		if msg.Event.HookEventName == "Stop" {
			cmds = append(cmds, m.collectProcesses)

			if classifying {
				gen := m.classifyGen[t.ID]
				m.pendingOps[t.ID] = ""
				cmds = append(cmds,
					m.classifyAttention(t.ID, t.Name, msg.Event.LastAssistantMessage, m.taskProcesses[t.ID], gen),
					m.spinner.Tick,
				)
			}
		}
		return m, tea.Batch(cmds...)

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

	case ProcessTickMsg:
		return m, tea.Batch(
			m.collectProcesses,
			m.processTick(),
		)

	case ProcessesUpdatedMsg:
		m.taskProcesses = msg.Processes
		return m, nil

	case SparklineTickMsg:
		return m, tea.Batch(
			m.fetchSparklineData,
			m.sparklineTick(),
		)

	case SparklineUpdatedMsg:
		m.sparklineData = msg.Data
		return m, nil

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

	case wizardCancelMsg:
		m.activeWizard = nil
		m.mode = ModeNormal
		return m, nil
	case wizardSubmitMsg:
		return m.handleWizardSubmit(msg)
	case wizardEditSubmitMsg:
		return m.handleWizardEditSubmit(msg)

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

	case wsDirCreatedMsg:
		if m.wsProgress == nil {
			return m, nil
		}
		m.wsProgress.WorkspaceDir = msg.WorkspaceDir
		m.wsProgress.LogLines = append(m.wsProgress.LogLines,
			fmt.Sprintf("Created workspace directory: %s", msg.WorkspaceDir))
		if len(m.wsProgress.Repos) == 0 {
			if m.wsProgress.Forking {
				m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Copying session files...")
				return m, m.wsForkCopySessionCmd()
			}
			// No repos — launch task immediately.
			if m.wsProgress.LaunchTask {
				m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Launching Claude...")
				return m, m.wsLaunchTaskCmd()
			}
			m.wsProgress.Done = true
			return m, nil
		}
		if m.wsProgress.Forking {
			// Start the first repo fork.
			m.wsProgress.Repos[0].Status = cloneStatusCloning
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Forking %s (%s)...", m.wsProgress.Repos[0].Repo, m.wsProgress.Repos[0].VCS))
			return m, m.wsForkRepoCmd(0, m.repoSets)
		}
		// Start the first clone.
		m.wsProgress.Repos[0].Status = cloneStatusCloning
		m.wsProgress.LogLines = append(m.wsProgress.LogLines,
			fmt.Sprintf("Cloning %s (%s)...", m.wsProgress.Repos[0].Repo, m.wsProgress.Repos[0].VCS))
		return m, m.wsCloneRepoCmd(0, m.repoSets)

	case wsCloneDoneMsg:
		if m.wsProgress == nil || msg.Index >= len(m.wsProgress.Repos) {
			return m, nil
		}
		entry := &m.wsProgress.Repos[msg.Index]
		if msg.Err != nil {
			entry.Status = cloneStatusFailed
			entry.Err = msg.Err
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Failed: %s — %v", entry.Repo, msg.Err))
		} else {
			entry.Status = cloneStatusDone
			entry.VCS = msg.VCS
			if output := strings.TrimSpace(msg.Output); output != "" {
				for _, line := range strings.Split(output, "\n") {
					m.wsProgress.LogLines = append(m.wsProgress.LogLines, line)
				}
			}
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Done: %s (%s)", entry.Repo, entry.VCS))
		}
		entry.Output = msg.Output

		// Check for cancellation.
		if m.wsProgress.Cancelled {
			if m.wsProgress.LaunchTask {
				// New task was cancelled — clean up workspace dir.
				m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Cancelled. Cleaning up...")
				_ = os.RemoveAll(m.wsProgress.WorkspaceDir)
			} else {
				m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Cancelled. Already-cloned repos kept.")
			}
			m.wsProgress.Done = true
			return m, nil
		}

		// Kick off next repo, or finish.
		nextIdx := msg.Index + 1
		if nextIdx < len(m.wsProgress.Repos) {
			m.wsProgress.Repos[nextIdx].Status = cloneStatusCloning
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Cloning %s (%s)...", m.wsProgress.Repos[nextIdx].Repo, m.wsProgress.Repos[nextIdx].VCS))
			return m, m.wsCloneRepoCmd(nextIdx, m.repoSets)
		}

		// All repos done. Launch task if needed.
		if m.wsProgress.LaunchTask {
			// Check if any repos succeeded.
			anySuccess := false
			for _, r := range m.wsProgress.Repos {
				if r.Status == cloneStatusDone {
					anySuccess = true
					break
				}
			}
			if !anySuccess {
				m.wsProgress.Err = fmt.Errorf("all repos failed")
				m.wsProgress.Done = true
				_ = os.RemoveAll(m.wsProgress.WorkspaceDir)
				return m, nil
			}
			m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Launching Claude...")
			return m, m.wsLaunchTaskCmd()
		}
		m.wsProgress.Done = true
		return m, nil

	case wsLaunchDoneMsg:
		if m.wsProgress == nil {
			return m, nil
		}
		if msg.Err != nil {
			m.wsProgress.Err = msg.Err
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Error: %v", msg.Err))
		} else {
			m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Done!")
		}
		m.wsProgress.Done = true
		return m, nil

	case wsCompleteDoneMsg:
		if m.wsProgress == nil {
			return m, nil
		}
		m.wsProgress.StoppingDone = true
		if msg.Err != nil {
			// Window could not be killed — abort without destroying
			// the workspace so the user can investigate.
			m.wsProgress.Err = msg.Err
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Error stopping Claude: %v", msg.Err))
			m.wsProgress.Done = true
			m.appendDebugLog(fmt.Sprintf("[%s] complete failed: %v",
				time.Now().Format("15:04:05"), msg.Err))
			return m, nil
		}
		m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Claude stopped.")

		// Start per-repo forget sequence.
		if len(m.wsProgress.Repos) > 0 {
			m.wsProgress.Repos[0].Status = cloneStatusCloning
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Forgetting workspace for %s...", m.wsProgress.Repos[0].Repo))
			return m, m.wsForgetRepoCmd(0, m.repoSets)
		}

		// Single-repo or no repos — try single-repo forget then remove.
		rs := m.repoSets
		if rs != nil && rs.WorkspaceStrategy == workspace.StrategySingleRepo {
			m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Forgetting jj workspace...")
			return m, m.wsForgetSingleRepoCmd(rs)
		}
		m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Removing workspace directory...")
		return m, m.wsRemoveDirCmd()

	case wsForgetDoneMsg:
		if m.wsProgress == nil || msg.Index >= len(m.wsProgress.Repos) {
			return m, nil
		}
		entry := &m.wsProgress.Repos[msg.Index]
		if msg.Err != nil {
			entry.Status = cloneStatusFailed
			entry.Err = msg.Err
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Warning: %s — %v", entry.Repo, msg.Err))
		} else {
			entry.Status = cloneStatusDone
			if output := strings.TrimSpace(msg.Output); output != "" {
				for _, line := range strings.Split(output, "\n") {
					m.wsProgress.LogLines = append(m.wsProgress.LogLines, line)
				}
			}
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Forgot: %s", entry.Repo))
		}
		entry.Output = msg.Output

		// Next repo or move to removal.
		nextIdx := msg.Index + 1
		if nextIdx < len(m.wsProgress.Repos) {
			m.wsProgress.Repos[nextIdx].Status = cloneStatusCloning
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Forgetting workspace for %s...", m.wsProgress.Repos[nextIdx].Repo))
			return m, m.wsForgetRepoCmd(nextIdx, m.repoSets)
		}

		m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Removing workspace directory...")
		return m, m.wsRemoveDirCmd()

	case wsRemoveDoneMsg:
		if m.wsProgress == nil {
			return m, nil
		}
		if msg.Err != nil {
			m.wsProgress.Err = msg.Err
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Warning: %v", msg.Err))
		} else {
			m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Done!")
		}
		m.wsProgress.Done = true
		return m, nil

	case wsForkRepoDoneMsg:
		if m.wsProgress == nil {
			return m, nil
		}
		// Handle initial error (e.g. workspace dir creation failure).
		if msg.Index < 0 {
			m.wsProgress.Err = msg.Err
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Error: %v", msg.Err))
			m.wsProgress.Done = true
			return m, nil
		}
		if msg.Index >= len(m.wsProgress.Repos) {
			return m, nil
		}
		entry := &m.wsProgress.Repos[msg.Index]
		if msg.Err != nil {
			entry.Status = cloneStatusFailed
			entry.Err = msg.Err
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Failed: %s — %v", entry.Repo, msg.Err))
		} else {
			entry.Status = cloneStatusDone
			entry.VCS = msg.VCS
			if output := strings.TrimSpace(msg.Output); output != "" {
				for _, line := range strings.Split(output, "\n") {
					m.wsProgress.LogLines = append(m.wsProgress.LogLines, line)
				}
			}
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Forked: %s (%s)", entry.Repo, entry.VCS))
		}
		entry.Output = msg.Output

		if m.wsProgress.Cancelled {
			m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Cancelled. Cleaning up...")
			_ = os.RemoveAll(m.wsProgress.WorkspaceDir)
			m.wsProgress.Done = true
			return m, nil
		}

		// Next repo or move to session copy.
		nextIdx := msg.Index + 1
		if nextIdx < len(m.wsProgress.Repos) {
			m.wsProgress.Repos[nextIdx].Status = cloneStatusCloning
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Forking %s (%s)...", m.wsProgress.Repos[nextIdx].Repo, m.wsProgress.Repos[nextIdx].VCS))
			return m, m.wsForkRepoCmd(nextIdx, m.repoSets)
		}

		// All repos forked — copy session files.
		m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Copying session files...")
		return m, m.wsForkCopySessionCmd()

	case wsForkSessionCopiedMsg:
		if m.wsProgress == nil {
			return m, nil
		}
		if msg.Err != nil {
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Warning copying session: %v", msg.Err))
		}
		m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Launching Claude...")
		return m, m.wsForkLaunchCmd()

	case wsForkLaunchDoneMsg:
		if m.wsProgress == nil {
			return m, nil
		}
		if msg.Err != nil {
			m.wsProgress.Err = msg.Err
			m.wsProgress.LogLines = append(m.wsProgress.LogLines,
				fmt.Sprintf("Error: %v", msg.Err))
		} else {
			m.wsProgress.LogLines = append(m.wsProgress.LogLines, "Done!")
		}
		m.wsProgress.Done = true
		return m, nil

	case forkSharedDoneMsg:
		delete(m.pendingOps, msg.PendingOpKey)
		if msg.Err != nil {
			m.appendDebugLog(fmt.Sprintf("[%s] fork error: %v",
				time.Now().Format("15:04:05"), msg.Err))
		}
		return m, m.refreshTasks

	case remoteSearchDebounceMsg:
		picker := m.activeRepoPicker
		if picker == nil && m.activeWizard != nil {
			picker = m.activeWizard.repoPicker
		}
		if picker == nil || msg.Generation != m.remoteSearchGen {
			return m, nil
		}
		r := &picker.remote
		r.searching = true
		org := r.activeOrg
		query := strings.TrimSpace(r.searchInput.Value())
		gen := msg.Generation
		return m, func() tea.Msg {
			repos, err := github.SearchRepos(org, query)
			return remoteSearchResultMsg{Generation: gen, Repos: repos, Err: err}
		}

	case remoteSearchResultMsg:
		picker := m.activeRepoPicker
		if picker == nil && m.activeWizard != nil {
			picker = m.activeWizard.repoPicker
		}
		if picker == nil || msg.Generation != m.remoteSearchGen {
			return m, nil
		}
		r := &picker.remote
		r.searching = false
		if msg.Err != nil {
			r.err = msg.Err
			r.results = nil
		} else {
			r.err = nil
			r.results = msg.Repos
		}
		r.cursor = 0
		return m, nil

	case remoteCloneDoneMsg:
		if m.returnToPickerOnClone && m.activeRepoPicker != nil {
			m.returnToPickerOnClone = false
			tp := m.activeRepoPicker
			tp.remote.cloning = false
			if msg.Err != nil {
				tp.remote.err = msg.Err
				m.workspaceProgressLines = nil
				m.mode = ModeRepoSelect
				return m, nil
			}
			tp.refreshLocalRepos()
			tp.switchToLocal()
			m.workspaceProgressLines = nil
			m.mode = ModeRepoSelect
			return m, nil
		}
		if m.returnToPickerOnClone && m.activeWizard != nil && m.activeWizard.repoPicker != nil {
			m.returnToPickerOnClone = false
			tp := m.activeWizard.repoPicker
			tp.remote.cloning = false
			if msg.Err != nil {
				tp.remote.err = msg.Err
				m.workspaceProgressLines = nil
				m.mode = ModeTaskWizard
				return m, nil
			}
			tp.refreshLocalRepos()
			tp.switchToLocal()
			m.workspaceProgressLines = nil
			m.mode = ModeTaskWizard
			return m, nil
		}
		// Fallthrough: treat as workspace progress done.
		m.workspaceProgressLines = nil
		m.mode = ModeNormal
		if msg.Err != nil {
			m.appendDebugLog(fmt.Sprintf("[%s] clone error: %v",
				time.Now().Format("15:04:05"), msg.Err))
		}
		return m, m.refreshTasks

	case classifyResultMsg:
		delete(m.pendingOps, msg.TaskID)
		if m.classifyGen[msg.TaskID] != msg.Generation {
			// Stale result — a newer hook event already arrived.
			return m, m.refreshTasks
		}
		if msg.Err != nil {
			m.appendDebugLog(fmt.Sprintf("[%s] classify: %s: %v",
				time.Now().Format("15:04:05"), msg.TaskID, msg.Err))
			// Fall back to waiting on error.
			return m, m.applyClassificationResult(msg.TaskID, db.AttentionWaiting)
		}
		if msg.NeedsAttention {
			return m, m.applyClassificationResult(msg.TaskID, db.AttentionWaiting)
		}
		return m, m.applyClassificationResult(msg.TaskID, db.AttentionDone)

	case usageResultMsg:
		delete(m.usageLoading, msg.TaskID)
		if msg.Err != nil {
			m.appendDebugLog(fmt.Sprintf("[%s] usage: %s: %v",
				time.Now().Format("15:04:05"), msg.TaskID, msg.Err))
			m.usageCache[msg.TaskID] = &usage.UsageSummary{Err: msg.Err}
		} else {
			m.usageCache[msg.TaskID] = msg.Usage
		}
		return m, nil

	case pendingOpDoneMsg:
		delete(m.pendingOps, msg.TaskID)
		return m, m.refreshTasks

	case spinner.TickMsg:
		wsActive := m.wsProgress != nil && !m.wsProgress.Done
		if len(m.pendingOps) > 0 || wsActive {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.mode == ModeForm && m.activeForm != nil {
		return m.handleFormUpdate(msg)
	}

	if m.mode == ModeTaskWizard && m.activeWizard != nil {
		cmd, resultMsg := m.activeWizard.Update(msg)
		if resultMsg != nil {
			switch r := resultMsg.(type) {
			case wizardCancelMsg:
				m.activeWizard = nil
				m.mode = ModeNormal
				return m, nil
			case wizardSubmitMsg:
				return m.handleWizardSubmit(r)
			case wizardEditSubmitMsg:
				return m.handleWizardEditSubmit(r)
			case wizardCloneRemoteMsg:
				return m.handleWizardCloneRemote(r)
			}
		}
		return m, cmd
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case ModeConfirmQuit:
		return m.handleConfirmQuitKey(msg)
	case ModeConfirmComplete:
		return m.handleConfirmCompleteKey(msg)
	case ModeConfirmFreeze:
		return m.handleConfirmFreezeKey(msg)
	case ModeDetail:
		return m.handleDetailKey(msg)
	case ModeForkDialog:
		return m.handleForkDialogKey(msg)
	case ModeHelp:
		return m.handleHelpKey(msg)
	case ModeSitRepLoading:
		return m, nil
	case ModeWorkspaceProgress:
		return m.handleWSProgressKey(msg)
	case ModeSitRep:
		return m.handleSitRepKey(msg)
	case ModeFilter:
		return m.handleFilterKey(msg)
	case ModeForm:
		return m.handleFormUpdate(msg)
	case ModeRepoSelect:
		return m.handleRepoSelectKey(msg)
	case ModeTaskWizard:
		return m.handleTaskWizardKey(msg)
	case ModeConfirmRelaunch:
		return m.handleConfirmRelaunchKey(msg)
	case ModeCommandPalette:
		return m.handleCommandPaletteKey(msg)
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
		if m.hasParkedTasks() {
			m.mode = ModeConfirmQuit
			return m, nil
		}
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
		baseDir, err := os.Getwd()
		if err != nil {
			return m, func() tea.Msg { return ErrorMsg{Err: err} }
		}
		w := newTaskWizard(
			m.taskStore.NameInUse,
			m.repoSets,
			m.cfg.SandboxProfileNames(),
			m.cfg.DefaultSandbox,
			baseDir,
			m.styles,
			m.styles.theme,
			m.huhTheme(),
		)
		m.activeWizard = w
		m.mode = ModeTaskWizard
		return m, w.Init()

	case "enter":
		return m, m.focusSelected()

	case "tab":
		if t := m.selectedTask(); t != nil {
			m.mode = ModeDetail
			return m, m.fetchUsageIfNeeded(t)
		}
		return m, nil

	case "s":
		m.sortByPriority = !m.sortByPriority
		m.cursor = 0
		return m, nil

	case "t":
		m.sparklineWindow = m.sparklineWindow.Next()
		return m, m.fetchSparklineData

	case ":":
		m.paletteCursor = 0
		m.mode = ModeCommandPalette
		return m, nil

	case "?":
		modalWidth := m.width * 2 / 3
		if modalWidth < 50 {
			modalWidth = 50
		}
		if modalWidth > m.width-4 {
			modalWidth = m.width - 4
		}
		// Inner width accounts for border (2) + padding (4).
		innerWidth := modalWidth - 6
		// Viewport height: terminal height minus border (2), padding (0),
		// and footer line (2), with some margin.
		vpHeight := m.height - 8
		if vpHeight < 6 {
			vpHeight = 6
		}

		m.helpViewport = viewport.New(innerWidth, vpHeight)
		m.helpViewport.SetContent(m.buildHelpContent())
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

type paletteCommand struct {
	Name string
	Desc string
	Run  func(m Model) (tea.Model, tea.Cmd)
}

func paletteCommands(m Model) []paletteCommand {
	return []paletteCommand{
		{
			Name: "Sit Rep",
			Desc: "Generate a briefing on all active tasks",
			Run: func(m Model) (tea.Model, tea.Cmd) {
				m.mode = ModeSitRepLoading
				return m, m.generateSitRep
			},
		},
		{
			Name: "Import",
			Desc: "Import an existing Claude Code session as a task",
			Run: func(m Model) (tea.Model, tea.Cmd) {
				form, result := newImportForm(m.taskStore.NameInUse, m.huhTheme())
				m.activeForm = form
				m.importFormResult = result
				m.mode = ModeForm
				return m, m.activeForm.Init()
			},
		},
		{
			Name: "Compact",
			Desc: "Renumber tmux windows sequentially",
			Run: func(m Model) (tea.Model, tea.Cmd) {
				m.mode = ModeNormal
				return m, m.compactWindows()
			},
		},
	}
}

func (m Model) handleCommandPaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmds := paletteCommands(m)
	switch msg.String() {
	case "esc", ":":
		m.mode = ModeNormal
		return m, nil
	case "j", "down":
		if m.paletteCursor < len(cmds)-1 {
			m.paletteCursor++
		}
		return m, nil
	case "k", "up":
		if m.paletteCursor > 0 {
			m.paletteCursor--
		}
		return m, nil
	case "enter":
		if m.paletteCursor >= 0 && m.paletteCursor < len(cmds) {
			return cmds[m.paletteCursor].Run(m)
		}
		m.mode = ModeNormal
		return m, nil
	}
	return m, nil
}

func (m Model) generateSitRep() tea.Msg {
	tasks, err := m.taskStore.List()
	if err != nil {
		return SitRepResultMsg{Err: err}
	}

	content, err := summary.GenerateSitRep(summary.SitRepInput{
		Tasks:      tasks,
		Processes:  m.taskProcesses,
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
		sandboxProfile := m.pendingSandboxProfile
		m.mode = ModeNormal
		m.pendingSandboxProfile = ""
		return m, func() tea.Msg {
			if err := m.taskStore.UpdateFlags(taskID, flags); err != nil {
				return ErrorMsg{Err: err}
			}
			if sandboxProfile != "" {
				if err := m.taskStore.UpdateSandboxProfile(taskID, sandboxProfile); err != nil {
					return ErrorMsg{Err: err}
				}
			}
			if err := m.manager.Relaunch(taskID); err != nil {
				return ErrorMsg{Err: err}
			}
			return m.refreshTasks()
		}
	default:
		m.mode = ModeNormal
		m.pendingSandboxProfile = ""
		return m, nil
	}
}

func (m Model) handleWSProgressKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ws := m.wsProgress
	if ws == nil {
		// Legacy path (remote clone in picker uses workspaceProgressLines).
		return m, nil
	}
	if ws.Done {
		switch msg.String() {
		case "esc":
			m.mode = ModeNormal
			m.wsProgress = nil
			if ws.Err != nil {
				m.appendDebugLog(fmt.Sprintf("[%s] workspace error: %v",
					time.Now().Format("15:04:05"), ws.Err))
			}
			return m, m.refreshTasks
		case "k", "up":
			maxOffset := len(ws.LogLines) - wsProgressMaxLogLines
			if maxOffset < 0 {
				maxOffset = 0
			}
			if ws.LogOffset < maxOffset {
				ws.LogOffset++
			}
		case "j", "down":
			if ws.LogOffset > 0 {
				ws.LogOffset--
			}
		}
		return m, nil
	}
	if msg.String() == "esc" && !ws.Destroying {
		ws.Cancelled = true
		ws.LogLines = append(ws.LogLines, "Cancelling...")
	}
	return m, nil
}

func (m Model) handleTaskWizardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	w := m.activeWizard
	if w == nil {
		m.mode = ModeNormal
		return m, nil
	}

	cmd, resultMsg := w.Update(msg)

	// Handle wizard-produced messages.
	switch r := resultMsg.(type) {
	case wizardCancelMsg:
		m.activeWizard = nil
		m.mode = ModeNormal
		return m, nil
	case wizardSubmitMsg:
		return m.handleWizardSubmit(r)
	case wizardEditSubmitMsg:
		return m.handleWizardEditSubmit(r)
	case wizardCloneRemoteMsg:
		return m.handleWizardCloneRemote(r)
	}

	// After remote search input changes, trigger debounced search.
	if w.repoPicker != nil && w.activeTab == wizardTabRepos {
		r := &w.repoPicker.remote
		if r.phase == remotePhaseSearch {
			query := strings.TrimSpace(r.searchInput.Value())
			lastQuery := strings.TrimSpace(w.lastSearchQuery)
			w.lastSearchQuery = query
			if query != lastQuery {
				if query != "" {
					m.remoteSearchGen++
					gen := m.remoteSearchGen
					r.searchGen = gen
					return m, tea.Batch(cmd, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg {
						return remoteSearchDebounceMsg{Generation: gen}
					}))
				}
				// Empty query — clear results.
				r.results = nil
				r.cursor = 0
				r.err = nil
				r.searching = false
			}
		}
	}

	return m, cmd
}

func (m Model) handleWizardSubmit(result wizardSubmitMsg) (tea.Model, tea.Cmd) {
	m.activeWizard = nil
	m.mode = ModeNormal

	rs := m.repoSets

	// Non-workspace flow: create task directly.
	if rs == nil || rs.WorkspaceStrategy == "" {
		return m, m.createTask(result.Name, "", result.Cwd, result.Flags, result.SandboxProfile)
	}

	// Workspace flow: create workspace then launch task.
	title := fmt.Sprintf("Creating workspace %q", result.Name)
	m.mode = ModeWorkspaceProgress
	m.initWSProgress(title, result.SelectedRepos, rs, true, result.Name, result.Flags, result.SandboxProfile)
	return m, tea.Batch(m.wsCreateDirAndStart(rs), m.spinner.Tick)
}

func (m Model) handleWizardEditSubmit(result wizardEditSubmitMsg) (tea.Model, tea.Cmd) {
	m.activeWizard = nil
	m.mode = ModeNormal

	taskID := result.TaskID

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

	// Check what changed.
	flagsChanged := result.Flags != originalTask.Flags

	originalProfile := originalTask.SandboxProfile
	if originalProfile == "" {
		originalProfile = m.cfg.DefaultSandbox
	}
	if originalProfile == "" {
		originalProfile = "none"
	}
	sandboxChanged := result.SandboxProfile != originalProfile

	hasRepos := len(result.SelectedRepos) > 0

	// If repos were selected, kick off the add-repos workspace flow.
	if hasRepos {
		rs := m.repoSets
		workspaceDir := originalTask.WorkspaceDir
		title := fmt.Sprintf("Adding repos to %q", originalTask.Name)
		m.mode = ModeWorkspaceProgress
		m.initWSProgress(title, result.SelectedRepos, rs, false, originalTask.Name, db.TaskFlags{}, "")
		m.wsProgress.WorkspaceDir = workspaceDir
		m.wsProgress.Repos[0].Status = cloneStatusCloning
		m.wsProgress.LogLines = append(m.wsProgress.LogLines,
			fmt.Sprintf("Cloning %s (%s)...", m.wsProgress.Repos[0].Repo, m.wsProgress.Repos[0].VCS))

		// Save flags/sandbox changes before starting clone.
		if flagsChanged {
			_ = m.taskStore.UpdateFlags(taskID, result.Flags)
		}
		if sandboxChanged {
			_ = m.taskStore.UpdateSandboxProfile(taskID, result.SandboxProfile)
		}
		return m, tea.Batch(m.wsCloneRepoCmd(0, rs), m.spinner.Tick)
	}

	// No repos — just flags/sandbox changes.
	if !flagsChanged && !sandboxChanged {
		return m, nil
	}

	// For dormant tasks, save directly.
	if originalTask.State == db.StateDormant {
		return m, func() tea.Msg {
			if flagsChanged {
				if err := m.taskStore.UpdateFlags(taskID, result.Flags); err != nil {
					return ErrorMsg{Err: err}
				}
			}
			if sandboxChanged {
				if err := m.taskStore.UpdateSandboxProfile(taskID, result.SandboxProfile); err != nil {
					return ErrorMsg{Err: err}
				}
			}
			return m.refreshTasks()
		}
	}

	// Active/parked: needs relaunch confirmation.
	m.pendingFlags = result.Flags
	m.pendingSandboxProfile = result.SandboxProfile
	m.flagEditTaskID = taskID
	m.mode = ModeConfirmRelaunch
	return m, nil
}

func (m Model) handleRepoSelectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	tp := m.activeRepoPicker

	// Global tab switching keys.
	switch msg.String() {
	case "tab":
		if tp.activeTab == pickerTabLocal {
			tp.switchToRemote()
		} else {
			tp.switchToLocal()
		}
		return m, nil
	}

	// Dispatch based on active tab.
	if tp.activeTab == pickerTabRemote {
		return m.handleRepoSelectRemoteKey(msg)
	}
	return m.handleRepoSelectLocalKey(msg)
}

func (m Model) handleRepoSelectLocalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.activeRepoPicker.local

	// Filter input mode: forward keys to the textinput.
	if p.filtering {
		switch msg.String() {
		case "esc":
			p.filtering = false
			p.filter.Blur()
			return m, nil
		case "enter":
			p.filtering = false
			p.filter.Blur()
			return m, nil
		}
		cmd := p.updateFilter(msg)
		return m, cmd
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
		return m, p.filter.Cursor.BlinkCmd()
	case "enter":
		selected := p.selectedRepos()

		// Add-repos flow — no point adding zero repos.
		if m.addReposTaskID != "" {
			if len(selected) == 0 {
				return m, nil
			}
			rs := m.repoSets
			workspaceDir := m.addReposWorkspaceDir
			taskName := filepath.Base(workspaceDir)
			m.activeRepoPicker = nil
			m.addReposTaskID = ""
			m.addReposWorkspaceDir = ""
			m.mode = ModeWorkspaceProgress
			title := fmt.Sprintf("Adding repos to %q", taskName)
			m.initWSProgress(title, selected, rs, false, taskName, db.TaskFlags{}, "")
			m.wsProgress.WorkspaceDir = workspaceDir
			// Start first clone directly (dir already exists).
			if len(selected) > 0 {
				m.wsProgress.Repos[0].Status = cloneStatusCloning
				m.wsProgress.LogLines = append(m.wsProgress.LogLines,
					fmt.Sprintf("Cloning %s (%s)...", m.wsProgress.Repos[0].Repo, m.wsProgress.Repos[0].VCS))
				return m, tea.Batch(m.wsCloneRepoCmd(0, rs), m.spinner.Tick)
			}
			m.wsProgress.Done = true
			return m, nil
		}

		// Repo select is now only used for the add-repos flow.
		// Task creation uses the wizard instead.
		m.activeRepoPicker = nil
		m.mode = ModeNormal
		return m, nil
	case "esc":
		// If there's filter text, clear it first.
		if strings.TrimSpace(p.filter.Value()) != "" {
			p.filter.Reset()
			p.refilter()
			return m, nil
		}
		m.activeRepoPicker = nil
		m.addReposTaskID = ""
		m.addReposWorkspaceDir = ""
		m.mode = ModeNormal
	}
	return m, nil
}

func (m Model) handleRepoSelectRemoteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	tp := m.activeRepoPicker
	r := &tp.remote

	// Handle enter on a search result — trigger clone.
	if msg.String() == "enter" && r.phase == remotePhaseSearch && len(r.results) > 0 {
		repo := tp.remoteSelectedRepo()
		if repo == "" {
			return m, nil
		}
		// Check if repo already exists locally.
		rs := m.repoSets
		if rs != nil {
			localRepos, _ := rs.ListRepos()
			for _, lr := range localRepos {
				if lr == repo {
					r.err = fmt.Errorf("%q already exists locally", repo)
					return m, nil
				}
			}
		}
		r.cloning = true
		m.returnToPickerOnClone = true
		m.mode = ModeWorkspaceProgress
		vcs := "git"
		if rs != nil && rs.DefaultVCS != "" {
			vcs = rs.DefaultVCS
		}
		m.workspaceProgressLines = []string{fmt.Sprintf("Cloning %s/%s (%s)...", r.activeOrg, repo, vcs)}
		return m, m.cloneRemoteRepo(r.activeOrg, repo, rs.ReposDir, vcs)
	}

	// Handle esc — cancel the picker if remote can't go further back.
	if msg.String() == "esc" {
		phaseBefore := r.phase
		cmd := tp.handleRemoteKey(msg)
		// If we're at the top-level phase and can't go back further,
		// cancel the whole picker.
		cancelPhase := remotePhaseOrgSelect
		if len(r.configOrgs) == 0 {
			cancelPhase = remotePhaseOrgEntry
		}
		if r.phase == cancelPhase && phaseBefore == cancelPhase {
			if cancelPhase == remotePhaseOrgEntry && strings.TrimSpace(r.orgInput.Value()) != "" {
				r.orgInput.Reset()
				return m, nil
			}
			m.activeRepoPicker = nil
			m.addReposTaskID = ""
			m.addReposWorkspaceDir = ""
			m.mode = ModeNormal
			return m, nil
		}
		return m, cmd
	}

	// All other keys go to the remote handler (org entry or search input).
	cmd := tp.handleRemoteKey(msg)

	// After search input changes, trigger debounced search.
	if r.phase == remotePhaseSearch && msg.String() != "j" && msg.String() != "k" &&
		msg.String() != "down" && msg.String() != "up" {
		query := strings.TrimSpace(r.searchInput.Value())
		if query != "" {
			m.remoteSearchGen++
			gen := m.remoteSearchGen
			r.searchGen = gen
			return m, tea.Batch(cmd, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg {
				return remoteSearchDebounceMsg{Generation: gen}
			}))
		}
		// Empty query — clear results.
		r.results = nil
		r.cursor = 0
		r.err = nil
		r.searching = false
	}

	return m, cmd
}

func (m Model) handleWizardCloneRemote(msg wizardCloneRemoteMsg) (tea.Model, tea.Cmd) {
	w := m.activeWizard
	if w == nil || w.repoPicker == nil {
		return m, nil
	}
	r := &w.repoPicker.remote
	rs := w.repoSets

	// Check if already exists locally.
	if rs != nil {
		localRepos, _ := rs.ListRepos()
		for _, lr := range localRepos {
			if lr == msg.Repo {
				r.err = fmt.Errorf("%q already exists locally", msg.Repo)
				return m, nil
			}
		}
	}

	r.cloning = true
	m.returnToPickerOnClone = true
	m.mode = ModeWorkspaceProgress
	vcs := "git"
	if rs != nil && rs.DefaultVCS != "" {
		vcs = rs.DefaultVCS
	}
	m.workspaceProgressLines = []string{fmt.Sprintf("Cloning %s/%s (%s)...", msg.Org, msg.Repo, vcs)}
	destDir := ""
	if rs != nil {
		destDir = rs.ReposDir
	}
	return m, m.cloneRemoteRepo(msg.Org, msg.Repo, destDir, vcs)
}

func (m Model) cloneRemoteRepo(org, repo, destDir, vcs string) tea.Cmd {
	return func() tea.Msg {
		err := github.CloneRepo(org, repo, destDir, vcs)
		return remoteCloneDoneMsg{RepoName: repo, Err: err}
	}
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
	case formTypeImport:
		if m.importFormResult == nil {
			return m, nil
		}
		result := m.importFormResult
		m.importFormResult = nil
		return m, m.importTask(result.Name, result.SessionID)

	}

	return m, nil
}

func (m Model) huhTheme() *huh.Theme {
	return huh.ThemeCatppuccin()
}

func (m Model) handleConfirmCompleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		t := m.selectedTask()
		if t == nil {
			m.mode = ModeNormal
			return m, nil
		}

		if t.WorkspaceDir == "" {
			// No workspace — use the simple pending-op spinner.
			m.mode = ModeNormal
			tick := m.startPendingOp(t.ID, "completing...")
			return m, tea.Batch(m.completeSelected(), tick)
		}

		// Check if other tasks share this workspace.
		shared, _ := m.taskStore.TasksSharingWorkspace(t.WorkspaceDir, t.ID)
		if len(shared) > 0 {
			// Shared workspace — complete the task but skip workspace cleanup.
			var names []string
			for _, s := range shared {
				names = append(names, s.Name)
			}
			m.mode = ModeNormal
			tick := m.startPendingOp(t.ID, "completing...")
			m.appendDebugLog(fmt.Sprintf("[%s] workspace cleanup skipped: shared with %s",
				time.Now().Format("15:04:05"), strings.Join(names, ", ")))
			return m, tea.Batch(m.completeSelected(), tick)
		}

		// Workspace task — use the rich progress modal.
		m.mode = ModeWorkspaceProgress
		rs := m.repoSets

		// Build repo list for the checklist (multi_repo only).
		var entries []repoCloneEntry
		if rs != nil && rs.WorkspaceStrategy == workspace.StrategyMultiRepo {
			repos := workspace.DestroyRepoList(t.WorkspaceDir)
			entries = make([]repoCloneEntry, len(repos))
			for i, repo := range repos {
				entries[i] = repoCloneEntry{Repo: repo, VCS: rs.DetectVCS(repo)}
			}
		}

		m.wsProgress = &wsProgressState{
			Title:        fmt.Sprintf("Completing %q", t.Name),
			Repos:        entries,
			Destroying:   true,
			TaskName:     t.Name,
			TaskID:       t.ID,
			WorkspaceDir: t.WorkspaceDir,
			LogLines:     []string{"Stopping Claude..."},
		}

		return m, tea.Batch(m.wsCompleteTaskCmd(t.ID), m.spinner.Tick)
	default:
		m.mode = ModeNormal
		return m, nil
	}
}

// checkWorkspaceWarnings checks git worktrees in the workspace for
// uncommitted changes and unpushed commits. Returns repo names that
// have each condition.
func checkWorkspaceWarnings(rs *workspace.RepoSets, t *db.Task) (uncommittedRepos, unpushedRepos []string) {
	if rs == nil || t.WorkspaceDir == "" {
		return nil, nil
	}

	checkRepo := func(worktreeDir, repoName string) {
		if workspace.HasUncommittedChanges(worktreeDir) {
			uncommittedRepos = append(uncommittedRepos, repoName)
		}
		if workspace.HasUnpushedCommits(worktreeDir) {
			unpushedRepos = append(unpushedRepos, repoName)
		}
	}

	switch rs.WorkspaceStrategy {
	case workspace.StrategySingleRepo:
		checkRepo(t.WorkspaceDir, t.Name)
	case workspace.StrategyMultiRepo:
		repos := workspace.PresentRepos(t.WorkspaceDir)
		for _, repo := range repos {
			if rs.DetectVCS(repo) != "jj" {
				checkRepo(filepath.Join(t.WorkspaceDir, repo), repo)
			}
		}
	}

	return uncommittedRepos, unpushedRepos
}

func (m Model) handleConfirmFreezeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		t := m.selectedTask()
		if t == nil {
			m.mode = ModeNormal
			return m, nil
		}
		m.mode = ModeNormal
		tick := m.startPendingOp(t.ID, "freezing...")
		return m, tea.Batch(m.dormifySelected(), tick)
	default:
		m.mode = ModeNormal
		return m, nil
	}
}

func (m Model) taskHasCompanions(t *db.Task) bool {
	session := m.activeSession
	if t.State == db.StateParked {
		session = m.parkedSession
	}
	return len(tmux.FindCompanions(session, t.Name)) > 0
}

func (m Model) handleConfirmQuitKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		return m, tea.Quit
	case "n", "N", "esc":
		m.mode = ModeNormal
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) hasParkedTasks() bool {
	for _, t := range m.tasks {
		if t.State == db.StateParked {
			return true
		}
	}
	return false
}

// startPendingOp sets a pending operation label on a task and starts the
// spinner animation. Returns the spinner tick command that must be batched
// with the action command.
func (m *Model) startPendingOp(taskID, label string) tea.Cmd {
	m.pendingOps[taskID] = label
	return m.spinner.Tick
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	t := m.selectedTask()
	if t == nil {
		m.mode = ModeNormal
		return m, nil
	}

	switch msg.String() {
	case "esc", "tab":
		m.mode = ModeNormal
		return m, nil

	case "enter":
		m.mode = ModeNormal
		return m, m.focusSelected()

	case "p":
		switch t.State {
		case db.StateActive:
			m.mode = ModeNormal
			tick := m.startPendingOp(t.ID, "parking...")
			return m, tea.Batch(m.parkSelected(), tick)
		case db.StateParked:
			m.mode = ModeNormal
			tick := m.startPendingOp(t.ID, "unparking...")
			return m, tea.Batch(m.unparkSelected(), tick)
		}
		return m, nil

	case "f":
		switch t.State {
		case db.StateActive, db.StateParked:
			if m.taskHasCompanions(t) {
				m.mode = ModeConfirmFreeze
				return m, nil
			}
			m.mode = ModeNormal
			tick := m.startPendingOp(t.ID, "freezing...")
			return m, tea.Batch(m.dormifySelected(), tick)
		case db.StateDormant:
			m.mode = ModeNormal
			tick := m.startPendingOp(t.ID, "unfreezing...")
			return m, tea.Batch(m.wakeSelected(), tick)
		}
		return m, nil

	case "c":
		m.confirmUncommittedRepos = nil
		m.confirmUnpushedRepos = nil
		if t.WorkspaceDir != "" {
			m.confirmUncommittedRepos, m.confirmUnpushedRepos = checkWorkspaceWarnings(m.repoSets, t)
		}
		m.mode = ModeConfirmComplete
		return m, nil

	case "+":
		if t.State == db.StateActive {
			m.mode = ModeNormal
			return m, m.createCompanion()
		}
		return m, nil

	case "d":
		// Fork: available for tasks with a session ID that aren't completed/failed.
		if t.SessionID == "" {
			return m, nil
		}
		if t.State == db.StateCompleted || t.State == db.StateFailed {
			return m, nil
		}
		return m.initForkDialog(t)

	case "e":
		if t.State == db.StateCompleted || t.State == db.StateFailed {
			return m, nil
		}
		var excludeRepos map[string]bool
		if t.WorkspaceDir != "" {
			present := workspace.PresentRepos(t.WorkspaceDir)
			excludeRepos = make(map[string]bool)
			for _, r := range present {
				excludeRepos[r] = true
			}
		}
		w := newEditWizard(
			t,
			m.repoSets,
			m.cfg.SandboxProfileNames(),
			m.cfg.DefaultSandbox,
			m.styles,
			m.styles.theme,
			m.huhTheme(),
			excludeRepos,
		)
		m.activeWizard = w
		m.mode = ModeTaskWizard
		return m, w.Init()
	}

	return m, nil
}

// Fork dialog and progress functions.

const (
	forkModeIndependent = iota
	forkModeShared
)

func (m Model) initForkDialog(t *db.Task) (tea.Model, tea.Cmd) {
	input := textinput.New()
	input.Placeholder = "fork name"
	input.CharLimit = 40
	input.Focus()
	input.SetValue(m.generateForkName(t.Name))

	m.forkNameInput = input
	m.forkMode = forkModeIndependent
	source := *t
	m.forkSourceTask = &source
	m.mode = ModeForkDialog
	return m, input.Focus()
}

func (m Model) generateForkName(sourceName string) string {
	// Try <source>-fork, then <source>-fork-2, etc.
	base := sourceName
	maxBase := 40 - len("-fork")
	if len(base) > maxBase {
		base = base[:maxBase]
	}

	candidate := base + "-fork"
	if !m.taskStore.NameInUse(candidate) {
		return candidate
	}

	for i := 2; i <= 99; i++ {
		suffix := fmt.Sprintf("-fork-%d", i)
		maxB := 40 - len(suffix)
		b := base
		if len(b) > maxB {
			b = b[:maxB]
		}
		candidate = b + suffix
		if !m.taskStore.NameInUse(candidate) {
			return candidate
		}
	}
	return base + "-fork"
}

func (m Model) forkHasWorkspace() bool {
	return m.forkSourceTask != nil && m.forkSourceTask.WorkspaceDir != ""
}

func (m Model) handleForkDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = ModeNormal
		m.forkSourceTask = nil
		return m, nil

	case "tab":
		// Toggle fork mode if workspace task.
		if m.forkHasWorkspace() {
			if m.forkMode == forkModeIndependent {
				m.forkMode = forkModeShared
			} else {
				m.forkMode = forkModeIndependent
			}
		}
		return m, nil

	case "enter":
		name := strings.TrimSpace(m.forkNameInput.Value())
		if name == "" {
			return m, nil
		}
		if m.taskStore.NameInUse(name) {
			return m, nil
		}
		return m.startFork(name)
	}

	// Forward other keys to the text input.
	var cmd tea.Cmd
	m.forkNameInput, cmd = m.forkNameInput.Update(msg)
	return m, cmd
}

func (m Model) startFork(name string) (tea.Model, tea.Cmd) {
	src := m.forkSourceTask
	if src == nil {
		m.mode = ModeNormal
		return m, nil
	}

	modeName := "independent"
	if m.forkMode == forkModeShared {
		modeName = "shared"
	}

	if m.forkMode == forkModeShared || src.WorkspaceDir == "" {
		// Shared mode or no workspace: launch directly, no workspace duplication.
		// No contested session needed — both tasks share the same cwd.
		m.mode = ModeNormal
		tick := m.startPendingOp("fork-"+name, "forking...")

		return m, tea.Batch(m.forkSharedCmd(name, src), tick)
	}

	// Independent mode: workspace duplication via progress modal.
	rs := m.repoSets
	if rs == nil {
		m.mode = ModeNormal
		return m, nil
	}

	// Mark the source session as contested BEFORE launching the fork.
	// The fork workspace path is deterministic.
	forkWorkspaceDir := filepath.Join(rs.WorkspacesDir, name)
	m.contestedSessions[src.SessionID] = forkWorkspaceDir

	var repos []string
	if rs.WorkspaceStrategy == workspace.StrategySingleRepo {
		// Single repo: find which source repo this workspace belongs to.
		// Check the workspace VCS and match against known repos.
		wsIsJJ := workspace.AllReposJJ(rs, src.WorkspaceDir)
		allRepos, _ := rs.ListRepos()
		for _, r := range allRepos {
			if wsIsJJ && rs.DetectVCS(r) == "jj" {
				repos = []string{r}
				break
			}
			if !wsIsJJ && rs.DetectVCS(r) != "jj" {
				repos = []string{r}
				break
			}
		}
		if len(repos) == 0 && len(allRepos) > 0 {
			repos = []string{allRepos[0]}
		}
	} else {
		repos = workspace.PresentRepos(src.WorkspaceDir)
	}

	entries := make([]repoCloneEntry, len(repos))
	for i, repo := range repos {
		entries[i] = repoCloneEntry{Repo: repo, VCS: rs.DetectVCS(repo)}
	}

	m.mode = ModeWorkspaceProgress
	m.wsProgress = &wsProgressState{
		Title:              fmt.Sprintf("Forking %q → %q", src.Name, name),
		Repos:              entries,
		Forking:            true,
		ForkMode:           modeName,
		SourceTaskID:       src.ID,
		SourceSessionID:    src.SessionID,
		SourceCwd:          sourceSessionCwd(src),
		TaskName:           name,
		TaskFlags:          src.Flags,
		TaskSandboxProfile: src.SandboxProfile,
	}

	return m, tea.Batch(m.wsForkCreateDirCmd(rs, name), m.spinner.Tick)
}

// forkSharedCmd launches a forked task that shares the source workspace.
func (m Model) forkSharedCmd(name string, src *db.Task) tea.Cmd {
	srcID := src.ID
	srcCwd := src.Cwd
	srcSessionID := src.SessionID
	srcFlags := src.Flags
	srcSandboxProfile := src.SandboxProfile
	srcWorkspaceDir := src.WorkspaceDir

	return func() tea.Msg {
		cwd := srcCwd
		workspaceDir := srcWorkspaceDir
		if workspaceDir != "" {
			cwd = workspaceDir
		}

		_, err := m.manager.ForkTask(name, srcSessionID, srcID, cwd, srcFlags, srcSandboxProfile, workspaceDir)
		return forkSharedDoneMsg{PendingOpKey: "fork-" + name, Err: err}
	}
}

// wsForkCreateDirCmd creates the workspace directory for a fork.
func (m Model) wsForkCreateDirCmd(rs *workspace.RepoSets, forkTaskName string) tea.Cmd {
	return func() tea.Msg {
		workspaceDir, err := workspace.CreateWorkspaceDir(rs, forkTaskName)
		if err != nil {
			return wsForkRepoDoneMsg{Index: -1, Err: err}
		}
		return wsDirCreatedMsg{WorkspaceDir: workspaceDir}
	}
}

// wsForkRepoCmd forks a single repo at the given index.
func (m Model) wsForkRepoCmd(index int, rs *workspace.RepoSets) tea.Cmd {
	ws := m.wsProgress
	if ws == nil || index >= len(ws.Repos) {
		return nil
	}
	entry := ws.Repos[index]

	srcWorkspaceDir := ws.SourceCwd
	if m.forkSourceTask != nil && m.forkSourceTask.WorkspaceDir != "" {
		srcWorkspaceDir = m.forkSourceTask.WorkspaceDir
	}

	dstPath := workspace.RepoDst(rs, ws.WorkspaceDir, entry.Repo)

	return func() tea.Msg {
		result := workspace.ForkRepo(rs, srcWorkspaceDir, dstPath, entry.Repo, ws.TaskName)
		return wsForkRepoDoneMsg{
			Index:  index,
			Output: result.Output,
			VCS:    result.VCS,
			Err:    result.Err,
		}
	}
}

// sourceSessionCwd returns the directory Claude's session file is keyed
// to. For workspace tasks the session was created from WorkspaceDir, but
// Cwd may have drifted to a repo subdirectory via hook updates.
func sourceSessionCwd(t *db.Task) string {
	if t.WorkspaceDir != "" {
		return t.WorkspaceDir
	}
	return t.Cwd
}

// wsForkCopySessionCmd copies session files for the fork.
func (m Model) wsForkCopySessionCmd() tea.Cmd {
	ws := m.wsProgress
	if ws == nil {
		return nil
	}
	return func() tea.Msg {
		err := task.CopySessionFiles(ws.SourceSessionID, ws.SourceCwd, ws.WorkspaceDir)
		return wsForkSessionCopiedMsg{Err: err}
	}
}

// wsForkLaunchCmd creates and launches the forked task.
func (m Model) wsForkLaunchCmd() tea.Cmd {
	ws := m.wsProgress
	if ws == nil {
		return nil
	}
	taskName := ws.TaskName
	sourceTaskID := ws.SourceTaskID
	sourceSessionID := ws.SourceSessionID
	workspaceDir := ws.WorkspaceDir
	flags := ws.TaskFlags
	sandboxProfile := ws.TaskSandboxProfile

	return func() tea.Msg {
		_, err := m.manager.ForkTask(taskName, sourceSessionID, sourceTaskID, workspaceDir, flags, sandboxProfile, workspaceDir)
		return wsForkLaunchDoneMsg{Err: err}
	}
}

const maxDebugLines = 20

func (m *Model) appendDebugLog(line string) {
	m.debugLog = append(m.debugLog, line)
	if len(m.debugLog) > maxDebugLines {
		m.debugLog = m.debugLog[len(m.debugLog)-maxDebugLines:]
	}
}

// tryAdoptSession matches an unknown session ID to an active task.
// This handles resumed sessions (new ID on SessionStart) and forked
// sessions (Claude assigns a new ID after --fork-session). Prefers
// tasks with empty session IDs (pending forks) over tasks that
// already have a session.
func (m *Model) tryAdoptSession(event hooks.HookEvent) *db.Task {
	tasks, err := m.taskStore.List()
	if err != nil {
		return nil
	}

	// First pass: prefer tasks with empty session ID (pending forks).
	// Match on cwd or workspace dir, since the event cwd may be
	// a subdirectory of the workspace.
	for _, t := range tasks {
		if t.State != db.StateActive {
			continue
		}
		if t.TmuxWindow == "" {
			continue
		}
		if t.SessionID != "" {
			continue
		}
		if !cwdMatchesTask(event.Cwd, t) {
			continue
		}
		_ = m.taskStore.UpdateSessionID(t.ID, event.SessionID)
		m.appendDebugLog(fmt.Sprintf("[%s] adopted forked session for task=%s",
			time.Now().Format("15:04:05"), t.Name))

		// Clean up contested session marker and copied session files.
		if t.SourceTaskID != "" {
			if srcTask, err := m.taskStore.Get(t.SourceTaskID); err == nil && srcTask != nil {
				_ = task.CleanupCopiedSession(srcTask.SessionID, t.Cwd)
				delete(m.contestedSessions, srcTask.SessionID)
			}
		}

		updated := t
		updated.SessionID = event.SessionID
		return &updated
	}

	// Second pass: tasks with existing session ID (cwd-based match for resumes).
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

// cwdMatchesTask reports whether an event's cwd belongs to the given
// task. For workspace tasks, the event cwd may be the workspace root
// or any subdirectory (e.g. a repo worktree inside the workspace).
func cwdMatchesTask(eventCwd string, t db.Task) bool {
	if eventCwd == "" {
		return false
	}
	if eventCwd == t.Cwd {
		return true
	}
	if t.WorkspaceDir != "" {
		return eventCwd == t.WorkspaceDir || strings.HasPrefix(eventCwd, t.WorkspaceDir+"/")
	}
	return false
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
	indexes := tmux.WindowIndexes(m.activeSession)
	return TasksRefreshedMsg{Tasks: tasks, WindowIndexes: indexes}
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
	_ = m.eventStore.TrimOlderThan(2 * time.Hour)
	return m.refreshTasks()
}

func (m Model) summaryTick() tea.Cmd {
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg {
		return SummaryTickMsg{}
	})
}

func (m Model) processTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		return ProcessTickMsg{}
	})
}

func (m Model) sparklineTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		return SparklineTickMsg{}
	})
}

func (m Model) fetchSparklineData() tea.Msg {
	taskIDs := make([]string, len(m.tasks))
	for i, t := range m.tasks {
		taskIDs[i] = t.ID
	}

	now := time.Now()
	since := now.Add(-m.sparklineWindow.Duration())
	events, err := m.eventStore.ActivityEvents(taskIDs, since)
	if err != nil {
		return ErrorMsg{Err: err}
	}

	data := make(map[string][]sparklineBucket)
	for _, id := range taskIDs {
		data[id] = bucketEvents(events[id], m.sparklineWindow.Duration(), sparklineWidth, now)
	}
	return SparklineUpdatedMsg{Data: data}
}

func (m Model) collectProcesses() tea.Msg {
	shellPIDs := make(map[string]int)
	for _, t := range m.tasks {
		if t.TmuxWindow == "" {
			continue
		}
		if t.State != db.StateActive && t.State != db.StateParked {
			continue
		}
		pid, err := tmux.PanePID(t.TmuxWindow)
		if err != nil {
			continue
		}
		shellPIDs[t.ID] = pid
	}

	if len(shellPIDs) == 0 {
		return ProcessesUpdatedMsg{Processes: nil}
	}
	return ProcessesUpdatedMsg{Processes: proctree.CollectAll(shellPIDs)}
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

	results := m.summaryPipeline.SummarizeAll(tasks, m.taskProcesses)
	for _, r := range results {
		debugLines = append(debugLines, fmt.Sprintf("[%s] summary: %s", now, r))
	}

	return SummariesUpdatedMsg{DebugLines: debugLines}
}

func (m Model) createTask(name, prompt, cwd string, flags db.TaskFlags, sandboxProfile string) tea.Cmd {
	return func() tea.Msg {
		if _, err := m.manager.CreateTask(name, prompt, cwd, flags, sandboxProfile); err != nil {
			return ErrorMsg{Err: err}
		}
		return m.refreshTasks()
	}
}

func (m *Model) initWSProgress(title string, repos []string, rs *workspace.RepoSets, launchTask bool, taskName string, flags db.TaskFlags, sandboxProfile string) {
	entries := make([]repoCloneEntry, len(repos))
	for i, repo := range repos {
		entries[i] = repoCloneEntry{
			Repo: repo,
			VCS:  rs.DetectVCS(repo),
		}
	}
	m.wsProgress = &wsProgressState{
		Title:              title,
		Repos:              entries,
		LaunchTask:         launchTask,
		TaskName:           taskName,
		TaskFlags:          flags,
		TaskSandboxProfile: sandboxProfile,
	}
}

// wsCreateDirAndStart creates the workspace dir, stores its path, and
// kicks off the first repo clone (or launches immediately if no repos).
func (m Model) wsCreateDirAndStart(rs *workspace.RepoSets) tea.Cmd {
	ws := m.wsProgress
	if ws == nil {
		return nil
	}
	return func() tea.Msg {
		workspaceDir, err := workspace.CreateWorkspaceDir(rs, ws.TaskName)
		if err != nil {
			return wsLaunchDoneMsg{Err: err}
		}
		// Smuggle the dir back via a dedicated msg so model can store it.
		return wsDirCreatedMsg{WorkspaceDir: workspaceDir}
	}
}

// wsCloneRepoCmd clones a single repo at the given index.
func (m Model) wsCloneRepoCmd(index int, rs *workspace.RepoSets) tea.Cmd {
	ws := m.wsProgress
	if ws == nil || index >= len(ws.Repos) {
		return nil
	}
	entry := ws.Repos[index]
	dst := workspace.RepoDst(rs, ws.WorkspaceDir, entry.Repo)
	taskName := ws.TaskName
	return func() tea.Msg {
		result := workspace.CloneRepo(rs, taskName, dst, entry.Repo)
		return wsCloneDoneMsg{
			Index:  index,
			Output: result.Output,
			VCS:    result.VCS,
			Err:    result.Err,
		}
	}
}

// wsLaunchTaskCmd creates the task and launches Claude.
func (m Model) wsLaunchTaskCmd() tea.Cmd {
	ws := m.wsProgress
	if ws == nil {
		return nil
	}
	name := ws.TaskName
	workspaceDir := ws.WorkspaceDir
	flags := ws.TaskFlags
	sandboxProfile := ws.TaskSandboxProfile
	return func() tea.Msg {
		t, err := m.manager.CreateTask(name, "", workspaceDir, flags, sandboxProfile)
		if err != nil {
			return wsLaunchDoneMsg{Err: err}
		}
		if err := m.taskStore.UpdateWorkspaceDir(t.ID, workspaceDir); err != nil {
			return wsLaunchDoneMsg{Err: err}
		}
		return wsLaunchDoneMsg{}
	}
}

// wsCompleteTaskCmd stops Claude via manager.Complete.
func (m Model) wsCompleteTaskCmd(taskID string) tea.Cmd {
	return func() tea.Msg {
		err := m.manager.Complete(taskID)
		return wsCompleteDoneMsg{Err: err}
	}
}

// wsForgetRepoCmd runs jj workspace forget for a single repo.
func (m Model) wsForgetRepoCmd(index int, rs *workspace.RepoSets) tea.Cmd {
	ws := m.wsProgress
	if ws == nil || index >= len(ws.Repos) {
		return nil
	}
	entry := ws.Repos[index]
	workspaceDir := ws.WorkspaceDir
	return func() tea.Msg {
		result := workspace.ForgetRepo(rs, workspaceDir, entry.Repo)
		return wsForgetDoneMsg{
			Index:  index,
			Output: result.Output,
			Err:    result.Err,
		}
	}
}

// wsForgetSingleRepoCmd forgets the jj workspace for a single_repo
// workspace, then removes the directory.
func (m Model) wsForgetSingleRepoCmd(rs *workspace.RepoSets) tea.Cmd {
	ws := m.wsProgress
	if ws == nil {
		return nil
	}
	workspaceDir := ws.WorkspaceDir
	return func() tea.Msg {
		_ = workspace.ForgetSingleRepoWorkspace(rs, workspaceDir)
		err := workspace.RemoveWorkspaceDir(workspaceDir)
		return wsRemoveDoneMsg{Err: err}
	}
}

// wsRemoveDirCmd removes the workspace directory.
func (m Model) wsRemoveDirCmd() tea.Cmd {
	ws := m.wsProgress
	if ws == nil {
		return nil
	}
	workspaceDir := ws.WorkspaceDir
	return func() tea.Msg {
		return wsRemoveDoneMsg{Err: workspace.RemoveWorkspaceDir(workspaceDir)}
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
	taskID := t.ID
	return func() tea.Msg {
		if err := m.manager.Park(taskID); err != nil {
			return ErrorMsg{Err: err}
		}
		return pendingOpDoneMsg{TaskID: taskID}
	}
}

func (m Model) unparkSelected() tea.Cmd {
	t := m.selectedTask()
	if t == nil {
		return nil
	}
	taskID := t.ID
	return func() tea.Msg {
		if err := m.manager.Unpark(taskID); err != nil {
			return ErrorMsg{Err: err}
		}
		return pendingOpDoneMsg{TaskID: taskID}
	}
}

func (m Model) dormifySelected() tea.Cmd {
	t := m.selectedTask()
	if t == nil {
		return nil
	}
	taskID := t.ID
	return func() tea.Msg {
		if err := m.manager.Dormify(taskID); err != nil {
			return ErrorMsg{Err: err}
		}
		return pendingOpDoneMsg{TaskID: taskID}
	}
}

func (m Model) wakeSelected() tea.Cmd {
	t := m.selectedTask()
	if t == nil {
		return nil
	}
	taskID := t.ID
	return func() tea.Msg {
		if err := m.manager.Wake(taskID); err != nil {
			return ErrorMsg{Err: err}
		}
		return pendingOpDoneMsg{TaskID: taskID}
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

func (m Model) handleHookEvent(event hooks.HookEvent, classifying bool, suppressAttention bool, taskID string) tea.Cmd {
	return func() tea.Msg {
		// Use the task ID resolved by the Update handler, which accounts
		// for contested sessions from forks. Do NOT re-lookup by session
		// ID here — that would bypass fork event routing.
		t, err := m.taskStore.Get(taskID)
		if err != nil || t == nil {
			return m.refreshTasks()
		}

		_ = m.eventStore.Log(t.ID, event.HookEventName, event.RawPayload)

		attention, ok := hooks.AttentionFromEvent(event)
		if ok {
			// Don't overwrite attention on terminal tasks — late
			// hook events (e.g. SessionEnd after Complete) must
			// not reset the attention state.
			if t.State == db.StateCompleted || t.State == db.StateFailed {
				ok = false
			}
			// When classification is in flight, don't set the
			// intermediate AttentionWaiting — let the classifier
			// decide the final state.
			if classifying && attention == db.AttentionWaiting {
				ok = false
			}
		}
		if ok {
			// When another agent still has a pending permission,
			// don't let lower-priority events downgrade to ok.
			if suppressAttention && attention != db.AttentionPermission && attention != db.AttentionError {
				ok = false
			}
		}
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

		return m.refreshTasks()
	}
}

func (m Model) applyClassificationResult(taskID string, attention db.AttentionState) tea.Cmd {
	return func() tea.Msg {
		_ = m.taskStore.UpdateAttention(taskID, attention)

		// Log a synthetic event so sparklines reflect the classification.
		switch attention {
		case db.AttentionDone:
			_ = m.eventStore.Log(taskID, "ClassifyDone", "")
		case db.AttentionWaiting:
			_ = m.eventStore.Log(taskID, "ClassifyWaiting", "")
		}

		if t, _ := m.taskStore.Get(taskID); t != nil && t.TmuxWindow != "" {
			applyWindowStyle(t.TmuxWindow, attention, m.cfg)
		}
		return m.refreshTasks()
	}
}

func (m Model) classifyAttention(taskID, taskName, lastMsg string, tp *proctree.TaskProcesses, gen uint64) tea.Cmd {
	return func() tea.Msg {
		procCtx := proctree.FormatForPrompt(tp)
		result, err := classify.Classify(taskName, lastMsg, procCtx)
		if err != nil {
			return classifyResultMsg{TaskID: taskID, Generation: gen, Err: err}
		}
		return classifyResultMsg{
			TaskID:         taskID,
			Generation:     gen,
			NeedsAttention: result.NeedsAttention,
		}
	}
}

func (m Model) fetchUsageIfNeeded(t *db.Task) tea.Cmd {
	if t.TranscriptPath == "" || m.usageCache[t.ID] != nil || m.usageLoading[t.ID] {
		return nil
	}
	m.usageLoading[t.ID] = true
	taskID := t.ID
	path := t.TranscriptPath
	return func() tea.Msg {
		summary, err := usage.ParseTranscript(path)
		return usageResultMsg{TaskID: taskID, Usage: summary, Err: err}
	}
}

func applyWindowStyle(windowID string, attention db.AttentionState, cfg config.Config) {
	_ = tmux.SetWindowOption(windowID, "krang-attn", string(attention))

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
		companionID, err := tmux.CreateWindowAfter(windowID, companionName, cwd)
		if err != nil {
			return ErrorMsg{Err: err}
		}
		_ = tmux.SetWindowOption(companionID, "krang-companion", taskName)
		return m.refreshTasks()
	}
}

func (m Model) compactWindows() tea.Cmd {
	session := m.activeSession
	return func() tea.Msg {
		if err := tmux.CompactWindows(session); err != nil {
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
	taskID := t.ID
	return func() tea.Msg {
		if err := m.manager.Complete(taskID); err != nil {
			return ErrorMsg{Err: err}
		}
		return pendingOpDoneMsg{TaskID: taskID}
	}
}
