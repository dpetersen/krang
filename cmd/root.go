package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dpetersen/krang/internal/config"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/hooks"
	"github.com/dpetersen/krang/internal/pathutil"
	"github.com/dpetersen/krang/internal/summary"
	"github.com/dpetersen/krang/internal/task"
	"github.com/dpetersen/krang/internal/tmux"
	"github.com/dpetersen/krang/internal/tui"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "krang",
	Short:         "Task orchestration for Claude Code sessions",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runTUI,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func runTUI(cmd *cobra.Command, args []string) error {
	if !tmux.InsideTmux() {
		return fmt.Errorf("krang must be run inside tmux")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	instanceID := pathutil.InstanceID(cwd)
	stateFilePath := pathutil.StateFilePath(cwd)
	krangSession := tmux.ActiveSessionName(instanceID)
	parkedSession := tmux.ParkedSessionName(instanceID)

	currentSession, err := tmux.CurrentSession()
	if err != nil {
		return fmt.Errorf("detecting current session: %w", err)
	}

	// If a session with our krang name already exists and it's not
	// this one, another krang TUI is already running for this directory.
	if currentSession != krangSession && tmux.SessionExists(krangSession) {
		return fmt.Errorf("krang is already running for this directory; attach with: tmux a -t %s", krangSession)
	}

	if currentSession != krangSession {
		if err := tmux.RenameSession(currentSession, krangSession); err != nil {
			return fmt.Errorf("renaming session: %w", err)
		}
	}

	if krangWindowID, err := tmux.CurrentWindowID(); err == nil {
		_ = tmux.RenameWindow(krangWindowID, "krang")
	}

	if err := tmux.EnsureParkedSession(parkedSession); err != nil {
		return fmt.Errorf("setting up parked session: %w", err)
	}

	configPath := config.Path()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Printf("No config found at %s — running first-time setup.\n\n", configPath)
		if err := runSetup(); err != nil {
			return err
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	database, err := db.Open(cwd)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer database.Close()

	taskStore := db.NewTaskStore(database)
	eventStore := db.NewEventStore(database)
	manager := task.NewManager(taskStore, eventStore, krangSession, parkedSession, cfg.SandboxCommand, stateFilePath)

	if err := manager.Reconcile(); err != nil {
		return fmt.Errorf("initial reconciliation: %w", err)
	}

	hookEvents := make(chan hooks.HookEvent, 64)
	hookServer := hooks.NewServer(stateFilePath, func(event hooks.HookEvent) {
		hookEvents <- event
	})
	if err := hookServer.Start(); err != nil {
		return fmt.Errorf("starting hook server: %w", err)
	}
	defer hookServer.Stop()

	summaryPipeline := summary.NewPipeline(taskStore)

	model := tui.NewModel(manager, taskStore, eventStore, hookEvents, summaryPipeline, krangSession, parkedSession, cfg)
	program := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}

	cleanupParkedSession(manager, parkedSession)

	return nil
}

func cleanupParkedSession(manager *task.Manager, parkedSession string) {
	if !tmux.SessionExists(parkedSession) {
		return
	}

	tasks, err := manager.ListTasks()
	if err != nil {
		return
	}

	var parkedTasks []db.Task
	for _, t := range tasks {
		if t.State == db.StateParked {
			parkedTasks = append(parkedTasks, t)
		}
	}

	if len(parkedTasks) == 0 {
		_ = tmux.KillSession(parkedSession)
		return
	}

	fmt.Printf("\n%d parked task(s) still running in the background:\n", len(parkedTasks))
	for _, t := range parkedTasks {
		fmt.Printf("  - %s\n", t.Name)
	}
	fmt.Print("\nFreeze them before exiting? [Y/n] ")

	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))

	if answer == "" || answer == "y" || answer == "yes" {
		for _, t := range parkedTasks {
			if err := manager.Dormify(t.ID); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: could not freeze %s: %s\n", t.Name, err)
			} else {
				fmt.Printf("  froze %s\n", t.Name)
			}
		}
		_ = tmux.KillSession(parkedSession)
	}
}
