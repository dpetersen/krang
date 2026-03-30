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
)

type formType int

const (
	formTypeNewTask formType = iota
	formTypeImport
	formTypeFlagEdit
	formTypeWorkspaceTask
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
