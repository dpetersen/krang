package tui

import (
	"fmt"
	"regexp"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/dpetersen/krang/internal/db"
)

var validTaskName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func validateTaskName(nameInUse func(string) bool) func(string) error {
	return func(name string) error {
		if name == "" {
			return fmt.Errorf("name is required")
		}
		if !validTaskName.MatchString(name) {
			return fmt.Errorf("name must be alphanumeric, hyphens, or underscores")
		}
		if nameInUse(name) {
			return fmt.Errorf("name %q is already in use", name)
		}
		return nil
	}
}

type taskCreationResult struct {
	Name  string
	Flags db.TaskFlags
}

func newTaskCreationForm(nameInUse func(string) bool, huhTheme *huh.Theme) (*huh.Form, *taskCreationResult) {
	result := &taskCreationResult{}
	var flagChoices []string

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Task name").
				Placeholder("my-task").
				CharLimit(40).
				Validate(validateTaskName(nameInUse)).
				Value(&result.Name),
		),
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Flags (optional)").
				Options(
					huh.NewOption("No Sandbox — launch claude directly", "no_sandbox"),
					huh.NewOption("Skip Permissions — --dangerously-skip-permissions", "skip_perms"),
					huh.NewOption("Debug — export KRANG_DEBUG=1 for relay logging", "debug"),
				).
				Value(&flagChoices),
		),
	).WithTheme(huhTheme)

	form.SubmitCmd = func() tea.Msg {
		for _, choice := range flagChoices {
			switch choice {
			case "no_sandbox":
				result.Flags.NoSandbox = true
			case "skip_perms":
				result.Flags.DangerouslySkipPermissions = true
			case "debug":
				result.Flags.Debug = true
			}
		}
		return formCompletedMsg{formType: formTypeNewTask}
	}
	form.CancelCmd = func() tea.Msg {
		return formCancelledMsg{}
	}

	return form, result
}

type importResult struct {
	Name      string
	SessionID string
}

func newImportForm(nameInUse func(string) bool, huhTheme *huh.Theme) (*huh.Form, *importResult) {
	result := &importResult{}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Task name").
				Placeholder("my-task").
				CharLimit(40).
				Validate(validateTaskName(nameInUse)).
				Value(&result.Name),
			huh.NewInput().
				Title("Claude session ID").
				Placeholder("UUID from Claude session").
				CharLimit(80).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("session ID is required")
					}
					return nil
				}).
				Value(&result.SessionID),
		),
	).WithTheme(huhTheme)

	form.SubmitCmd = func() tea.Msg {
		return formCompletedMsg{formType: formTypeImport}
	}
	form.CancelCmd = func() tea.Msg {
		return formCancelledMsg{}
	}

	return form, result
}

type flagEditResult struct {
	Flags db.TaskFlags
}

func newFlagEditForm(currentFlags db.TaskFlags, taskName string, huhTheme *huh.Theme) (*huh.Form, *flagEditResult) {
	result := &flagEditResult{}
	var flagChoices []string

	if currentFlags.NoSandbox {
		flagChoices = append(flagChoices, "no_sandbox")
	}
	if currentFlags.DangerouslySkipPermissions {
		flagChoices = append(flagChoices, "skip_perms")
	}
	if currentFlags.Debug {
		flagChoices = append(flagChoices, "debug")
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Flags: " + taskName).
				Options(
					huh.NewOption("No Sandbox — launch claude directly", "no_sandbox"),
					huh.NewOption("Skip Permissions — --dangerously-skip-permissions", "skip_perms"),
					huh.NewOption("Debug — export KRANG_DEBUG=1 for relay logging", "debug"),
				).
				Value(&flagChoices),
		),
	).WithTheme(huhTheme)

	form.SubmitCmd = func() tea.Msg {
		result.Flags = db.TaskFlags{}
		for _, choice := range flagChoices {
			switch choice {
			case "no_sandbox":
				result.Flags.NoSandbox = true
			case "skip_perms":
				result.Flags.DangerouslySkipPermissions = true
			case "debug":
				result.Flags.Debug = true
			}
		}
		return formCompletedMsg{formType: formTypeFlagEdit}
	}
	form.CancelCmd = func() tea.Msg {
		return formCancelledMsg{}
	}

	return form, result
}
