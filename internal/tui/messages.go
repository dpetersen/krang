package tui

import (
	"github.com/dpetersen/krang/internal/db"
)

type TasksRefreshedMsg struct {
	Tasks []db.Task
}

type ErrorMsg struct {
	Err error
}

type ReconcileTickMsg struct{}

type InputMode int

const (
	ModeNormal InputMode = iota
	ModeNewName
	ModeNewPrompt
	ModeConfirmKill
)
