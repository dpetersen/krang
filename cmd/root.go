package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dpetersen/krang/internal/config"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/hooks"
	"github.com/dpetersen/krang/internal/summary"
	"github.com/dpetersen/krang/internal/task"
	"github.com/dpetersen/krang/internal/tmux"
	"github.com/dpetersen/krang/internal/tui"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "krang",
	Short: "Task orchestration for Claude Code sessions",
	RunE:  runTUI,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runTUI(cmd *cobra.Command, args []string) error {
	if !tmux.InsideTmux() {
		return fmt.Errorf("krang must be run inside tmux")
	}

	activeSession, err := tmux.CurrentSession()
	if err != nil {
		return fmt.Errorf("detecting current session: %w", err)
	}

	if krangWindowID, err := tmux.CurrentWindowID(); err == nil {
		_ = tmux.RenameWindow(krangWindowID, "krang")
	}

	if err := tmux.EnsureParkedSession(); err != nil {
		return fmt.Errorf("setting up parked session: %w", err)
	}

	cfg, err := config.Load(config.Path())
	if err != nil {
		return err
	}

	database, err := db.Open()
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer database.Close()

	taskStore := db.NewTaskStore(database)
	eventStore := db.NewEventStore(database)
	manager := task.NewManager(taskStore, eventStore, activeSession, cfg.SandboxCommand)

	if err := manager.Reconcile(); err != nil {
		return fmt.Errorf("initial reconciliation: %w", err)
	}

	hookEvents := make(chan hooks.HookEvent, 64)
	hookServer := hooks.NewServer(func(event hooks.HookEvent) {
		hookEvents <- event
	})
	if err := hookServer.Start(); err != nil {
		return fmt.Errorf("starting hook server on %s: %w", hooks.ListenAddr, err)
	}
	defer hookServer.Stop()

	summaryPipeline := summary.NewPipeline(taskStore)

	model := tui.NewModel(manager, taskStore, eventStore, hookEvents, summaryPipeline, activeSession, cfg)
	program := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}

	return nil
}
