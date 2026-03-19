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

type InputMode int

const (
	ModeNormal InputMode = iota
	ModeNewName
	ModeNewPrompt
	ModeConfirmKill
)
