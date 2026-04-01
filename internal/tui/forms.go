package tui

import (
	"fmt"
	"os"
	"regexp"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
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

const sandboxNone = "(none)"

func cwdOptions(baseDir string) []huh.Option[string] {
	opts := []huh.Option[string]{
		huh.NewOption(".  (current directory)", "."),
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return opts
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		opts = append(opts, huh.NewOption(name, name))
	}
	return opts
}

func sandboxProfileOptions(profiles []string) []huh.Option[string] {
	options := make([]huh.Option[string], 0, len(profiles)+1)
	for _, name := range profiles {
		options = append(options, huh.NewOption(name, name))
	}
	options = append(options, huh.NewOption(sandboxNone, sandboxNone))
	return options
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

