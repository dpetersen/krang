package tui

import (
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/hooks"
)

type TasksRefreshedMsg struct {
	Tasks []db.Task
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

type SummariesUpdatedMsg struct {
	DebugLines []string
}

type InputMode int

const (
	ModeNormal InputMode = iota
	ModeConfirmKill
	ModeHelp
	ModeFilter
	ModeSitRep
	ModeSitRepLoading
	ModeConfirmRelaunch
	ModeForm
	ModeWorkspaceProgress
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
