package tui

import (
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/hooks"
	"github.com/dpetersen/krang/internal/proctree"
	"github.com/dpetersen/krang/internal/usage"
)

type TasksRefreshedMsg struct {
	Tasks         []db.Task
	WindowIndexes map[string]string // tmux window ID → display index
}

type ErrorMsg struct {
	Err error
}

type HookEventMsg struct {
	Event hooks.HookEvent
}

type ReconcileTickMsg struct{}

type SitRepResultMsg struct {
	Content string
	Err     error
}

type SummaryTickMsg struct{}

type ProcessTickMsg struct{}

type ProcessesUpdatedMsg struct {
	Processes map[string]*proctree.TaskProcesses
}

type SummariesUpdatedMsg struct {
	DebugLines []string
}

type InputMode int

const (
	ModeNormal InputMode = iota
	ModeConfirmComplete
	ModeDetail
	ModeHelp
	ModeFilter
	ModeSitRep
	ModeSitRepLoading
	ModeConfirmRelaunch
	ModeForm
	ModeRepoSelect
	ModeWorkspaceProgress
	ModeCommandPalette
	ModeConfirmQuit
	ModeConfirmFreeze
	ModeTaskWizard
	ModeForkDialog
)

type formType int

const (
	formTypeImport formType = iota
)

type formCompletedMsg struct {
	formType formType
}

type formCancelledMsg struct{}

type workspaceProgressMsg struct {
	Lines []string
	Done  bool
	Err   error
}

// repoCloneStatus tracks the state of a single repo clone.
type repoCloneStatus int

const (
	cloneStatusPending repoCloneStatus = iota
	cloneStatusCloning
	cloneStatusDone
	cloneStatusFailed
)

// repoCloneEntry tracks one repo within the progress modal.
type repoCloneEntry struct {
	Repo   string
	VCS    string
	Status repoCloneStatus
	Output string // clone output (on success or failure)
	Err    error
}

// wsProgressState holds the full state for the workspace progress modal.
type wsProgressState struct {
	Title        string
	Repos        []repoCloneEntry
	LogLines     []string // scrollable log output
	Done         bool
	Cancelled    bool
	Err          error
	LaunchTask   bool // true if we should launch Claude after cloning
	Destroying   bool // true if this is a destroy/complete operation
	StoppingDone bool // true once Claude has been stopped
	TaskName           string
	TaskID             string
	TaskFlags          db.TaskFlags
	TaskSandboxProfile string
	WorkspaceDir       string

	// Fork-specific fields.
	Forking        bool   // true if this is a fork operation
	ForkMode       string // "independent" or "shared"
	SourceTaskID   string // source task's ID (for lineage tracking)
	SourceSessionID string // source task's session ID (for file copy)
	SourceCwd      string // source task's cwd (for session file copy)
}

// wsDirCreatedMsg signals that the workspace directory was created.
type wsDirCreatedMsg struct {
	WorkspaceDir string
}

// wsCloneDoneMsg signals that a single repo clone has completed.
type wsCloneDoneMsg struct {
	Index  int
	Output string
	VCS    string
	Err    error
}

// wsLaunchDoneMsg signals that the task launch step completed.
type wsLaunchDoneMsg struct {
	Err error
}

// wsCompleteDoneMsg signals that manager.Complete finished (Claude stopped).
type wsCompleteDoneMsg struct {
	Err error
}

// wsForgetDoneMsg signals that a single repo forget has completed.
type wsForgetDoneMsg struct {
	Index  int
	Output string
	Err    error
}

// wsRemoveDoneMsg signals that the workspace dir removal is done.
type wsRemoveDoneMsg struct {
	Err error
}

// wsForkRepoDoneMsg signals that a single repo fork has completed.
type wsForkRepoDoneMsg struct {
	Index  int
	Output string
	VCS    string
	Err    error
}

// wsForkSessionCopiedMsg signals that session files were copied.
type wsForkSessionCopiedMsg struct {
	Err error
}

// wsForkLaunchDoneMsg signals that the forked task was launched.
type wsForkLaunchDoneMsg struct {
	Err error
}

// forkSharedDoneMsg signals that a shared-mode fork completed.
type forkSharedDoneMsg struct {
	PendingOpKey string
	Err          error
}

type pendingOpDoneMsg struct {
	TaskID string
}

type classifyResultMsg struct {
	TaskID         string
	Generation     uint64
	NeedsAttention bool
	Err            error
}

type SparklineTickMsg struct{}

type SparklineUpdatedMsg struct {
	Data map[string][]sparklineBucket
}

type remoteSearchDebounceMsg struct {
	Generation uint64
}

type remoteSearchResultMsg struct {
	Generation uint64
	Repos      []string
	Err        error
}

type remoteCloneDoneMsg struct {
	RepoName string
	Err      error
}

type usageResultMsg struct {
	TaskID string
	Usage  *usage.UsageSummary
	Err    error
}
