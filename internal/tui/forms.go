package tui

import (
	"fmt"
	"os"
	"path/filepath"
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

const sandboxNone = "(none)"

type taskCreationResult struct {
	Name           string
	Cwd            string
	Flags          db.TaskFlags
	SandboxProfile string
}

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

func newTaskCreationForm(nameInUse func(string) bool, baseDir string, sandboxProfiles []string, defaultSandbox string, huhTheme *huh.Theme) (*huh.Form, *taskCreationResult) {
	result := &taskCreationResult{Cwd: "."}
	var flagChoices []string

	dirOptions := cwdOptions(baseDir)

	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewInput().
				Title("Task name").
				Placeholder("my-task").
				CharLimit(40).
				Validate(validateTaskName(nameInUse)).
				Value(&result.Name),
		),
	}

	if len(sandboxProfiles) > 0 {
		result.SandboxProfile = defaultSandbox
		profileOptions := sandboxProfileOptions(sandboxProfiles)
		groups = append(groups, huh.NewGroup(
			huh.NewSelect[string]().
				Title("Sandbox profile").
				Options(profileOptions...).
				Value(&result.SandboxProfile),
		))
	}

	groups = append(groups, huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Flags (optional)").
			Options(
				huh.NewOption("Skip Permissions — --dangerously-skip-permissions", "skip_perms"),
				huh.NewOption("Debug — export KRANG_DEBUG=1 for relay logging", "debug"),
			).
			Value(&flagChoices),
	))

	// Only show the CWD picker if there are subdirectories to pick from.
	if len(dirOptions) > 1 {
		groups = append(groups, huh.NewGroup(
			huh.NewSelect[string]().
				Title("Working directory").
				Options(dirOptions...).
				Value(&result.Cwd),
		))
	}

	form := huh.NewForm(groups...).WithTheme(huhTheme)

	form.SubmitCmd = func() tea.Msg {
		for _, choice := range flagChoices {
			switch choice {
			case "skip_perms":
				result.Flags.DangerouslySkipPermissions = true
			case "debug":
				result.Flags.Debug = true
			}
		}
		if result.SandboxProfile == sandboxNone {
			result.SandboxProfile = "none"
		}
		// Resolve the selected cwd to an absolute path.
		if result.Cwd == "." {
			result.Cwd = baseDir
		} else {
			result.Cwd = filepath.Join(baseDir, result.Cwd)
		}
		return formCompletedMsg{formType: formTypeNewTask}
	}
	form.CancelCmd = func() tea.Msg {
		return formCancelledMsg{}
	}

	return form, result
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

type workspaceTaskResult struct {
	Name           string
	Flags          db.TaskFlags
	SandboxProfile string
	SelectedRepos  []string
}

func newWorkspaceTaskForm(nameInUse func(string) bool, availableRepos []string, singleRepo bool, sandboxProfiles []string, defaultSandbox string, huhTheme *huh.Theme) (*huh.Form, *workspaceTaskResult) {
	result := &workspaceTaskResult{}
	var flagChoices []string
	var selectedRepo string

	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewInput().
				Title("Task name").
				Placeholder("my-task").
				CharLimit(40).
				Validate(validateTaskName(nameInUse)).
				Value(&result.Name),
		),
	}

	if len(sandboxProfiles) > 0 {
		result.SandboxProfile = defaultSandbox
		profileOptions := sandboxProfileOptions(sandboxProfiles)
		groups = append(groups, huh.NewGroup(
			huh.NewSelect[string]().
				Title("Sandbox profile").
				Options(profileOptions...).
				Value(&result.SandboxProfile),
		))
	}

	groups = append(groups, huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Flags (optional)").
			Options(
				huh.NewOption("Skip Permissions — --dangerously-skip-permissions", "skip_perms"),
				huh.NewOption("Debug — export KRANG_DEBUG=1 for relay logging", "debug"),
			).
			Value(&flagChoices),
	))

	// single_repo includes the repo picker inline; multi_repo uses
	// the custom repo picker component after the form completes.
	if singleRepo {
		repoOptions := []huh.Option[string]{
			huh.NewOption("(none — empty workspace)", ""),
		}
		for _, r := range availableRepos {
			repoOptions = append(repoOptions, huh.NewOption(r, r))
		}
		groups = append(groups, huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select repo").
				Options(repoOptions...).
				Value(&selectedRepo),
		))
	}

	form := huh.NewForm(groups...).WithTheme(huhTheme)

	form.SubmitCmd = func() tea.Msg {
		for _, choice := range flagChoices {
			switch choice {
			case "skip_perms":
				result.Flags.DangerouslySkipPermissions = true
			case "debug":
				result.Flags.Debug = true
			}
		}
		if result.SandboxProfile == sandboxNone {
			result.SandboxProfile = "none"
		}
		if singleRepo && selectedRepo != "" {
			result.SelectedRepos = []string{selectedRepo}
		}
		return formCompletedMsg{formType: formTypeWorkspaceTask}
	}
	form.CancelCmd = func() tea.Msg {
		return formCancelledMsg{}
	}

	return form, result
}

type flagEditResult struct {
	Flags          db.TaskFlags
	SandboxProfile string
}

func newFlagEditForm(currentFlags db.TaskFlags, currentSandboxProfile string, sandboxProfiles []string, defaultSandbox string, taskName string, huhTheme *huh.Theme) (*huh.Form, *flagEditResult) {
	result := &flagEditResult{}
	var flagChoices []string

	if currentFlags.DangerouslySkipPermissions {
		flagChoices = append(flagChoices, "skip_perms")
	}
	if currentFlags.Debug {
		flagChoices = append(flagChoices, "debug")
	}

	// Resolve the displayed sandbox profile.
	displayProfile := currentSandboxProfile
	if displayProfile == "" {
		displayProfile = defaultSandbox
	}
	if displayProfile == "none" || displayProfile == "" {
		displayProfile = sandboxNone
	}
	result.SandboxProfile = displayProfile

	var groups []*huh.Group

	if len(sandboxProfiles) > 0 {
		profileOptions := sandboxProfileOptions(sandboxProfiles)
		groups = append(groups, huh.NewGroup(
			huh.NewSelect[string]().
				Title("Sandbox profile: "+taskName).
				Options(profileOptions...).
				Value(&result.SandboxProfile),
		))
	}

	groups = append(groups, huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Flags: "+taskName).
			Options(
				huh.NewOption("Skip Permissions — --dangerously-skip-permissions", "skip_perms"),
				huh.NewOption("Debug — export KRANG_DEBUG=1 for relay logging", "debug"),
			).
			Value(&flagChoices),
	))

	form := huh.NewForm(groups...).WithTheme(huhTheme)

	form.SubmitCmd = func() tea.Msg {
		result.Flags = db.TaskFlags{}
		for _, choice := range flagChoices {
			switch choice {
			case "skip_perms":
				result.Flags.DangerouslySkipPermissions = true
			case "debug":
				result.Flags.Debug = true
			}
		}
		if result.SandboxProfile == sandboxNone {
			result.SandboxProfile = "none"
		}
		return formCompletedMsg{formType: formTypeFlagEdit}
	}
	form.CancelCmd = func() tea.Msg {
		return formCancelledMsg{}
	}

	return form, result
}
