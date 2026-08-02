package cmd

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/dpetersen/krang/internal/config"
	"github.com/dpetersen/krang/internal/db"
	"github.com/dpetersen/krang/internal/hooks"
	"github.com/dpetersen/krang/internal/pathutil"
	"github.com/dpetersen/krang/internal/summary"
	"github.com/dpetersen/krang/internal/task"
	"github.com/dpetersen/krang/internal/tmux"
	"github.com/dpetersen/krang/internal/tui"
	"github.com/dpetersen/krang/internal/workspace"
	"github.com/dpetersen/krang/internal/workspaceclient"
	"github.com/spf13/cobra"
)

var Version = "dev"

var rootCmd = &cobra.Command{
	Use:           "krang",
	Short:         "Task orchestration for Claude Code sessions",
	Version:       Version,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runTUI,
}

// Execute runs the CLI. A command that needs to say more than
// "succeeded or didn't" returns an error carrying an ExitCode method;
// those have already written their own diagnostics, so the code is
// passed on without another line of output. Everything else is a plain
// error and exits 1.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		var coded interface{ ExitCode() int }
		if errors.As(err, &coded) {
			os.Exit(coded.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

// nestedLaunchMessage explains the one launch krang refuses outright.
//
// A task window has KRANG_STATEFILE set, which is how everything inside
// it finds the instance that owns it. Starting the TUI there renames that
// session to this new instance's name, and from that moment the owning
// instance is looking for windows in a session that no longer answers to
// the name it knows. There is no override: two krangs in one window is
// not a thing anyone wants, so a flag for it would only ever be found by
// accident.
const nestedLaunchMessage = `KRANG_STATEFILE is set, so this is a krang task window.

Launching the TUI here would rename the tmux session out from under the
krang instance that owns this window, leaving both unable to find their
own windows. There is no override flag — a krang inside a krang has no
use case.

To manage this task's workspace from in here, use the subcommands, which
talk to the running instance instead of starting a new one:

    krang workspace list

If you deliberately want a second instance for this directory, drop the
variable first:

    env -u KRANG_STATEFILE krang`

// launchGuards are the questions a bare "krang" has to answer before it
// is allowed to change anything at all.
//
// They live in one struct with injectable probes because the order they
// run in is the whole point: every one of them used to be answered after
// krang had already renamed the caller's tmux session, created a parked
// session, and opened its database. The terminal check in particular was
// not a check — it was Bubble Tea's Run() failing at the very end.
type launchGuards struct {
	// Statefile is KRANG_STATEFILE. Non-empty means a task window.
	Statefile string
	// TerminalAvailable reports whether the TUI can get a terminal.
	TerminalAvailable func() bool
	// InsideTmux reports whether there is a tmux server to run in. This
	// is the first probe that shells out to tmux, so it is deliberately
	// last: a refused launch never runs a tmux command at all.
	InsideTmux func() bool
}

// Probes as package vars so the guard can be exercised without a real
// terminal or a real tmux server to fail against.
var (
	terminalAvailable = interactiveTerminalAvailable
	insideTmux        = tmux.InsideTmux
)

func currentLaunchGuards() launchGuards {
	return launchGuards{
		Statefile:         os.Getenv(workspaceclient.StateFileEnv),
		TerminalAvailable: terminalAvailable,
		InsideTmux:        insideTmux,
	}
}

func (g launchGuards) check() error {
	if g.Statefile != "" {
		return errors.New(nestedLaunchMessage)
	}
	if !g.TerminalAvailable() {
		return errors.New("krang is an interactive TUI, but stdin is not a terminal and /dev/tty could not be opened")
	}
	if !g.InsideTmux() {
		return errors.New("krang must be run inside tmux")
	}
	return nil
}

// interactiveTerminalAvailable asks the question Bubble Tea asks inside
// Run(): it drives the TUI from stdin when stdin is a terminal, and
// otherwise opens /dev/tty. When neither works Run fails with "could not
// open a new TTY" — correct, but by then krang has renamed the session,
// created the parked session, written a state file, and created its
// database. Asking here costs a stat and an open and moves that failure
// ahead of all of it.
func interactiveTerminalAvailable() bool {
	if term.IsTerminal(os.Stdin.Fd()) {
		return true
	}
	f, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func runTUI(cmd *cobra.Command, args []string) error {
	// Nothing below this line may run before the guards pass: everything
	// from here to the end of the function either renames something,
	// creates something, or binds a port.
	if err := currentLaunchGuards().check(); err != nil {
		return err
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
	// this one, another krang instance may be running for this
	// directory. Verify via the hook server health check to
	// distinguish a live instance from a stale session.
	if currentSession != krangSession && tmux.SessionExists(krangSession) {
		if hooks.InstanceIsLive(stateFilePath) {
			return fmt.Errorf("krang is already running for this directory; attach with: tmux a -t %s", krangSession)
		}
		return fmt.Errorf("a stale krang tmux session exists from a previous run: %s\nkill it with: tmux kill-session -t %s", krangSession, krangSession)
	}

	// A leftover parked session (without a matching active session) is
	// harmless — EnsureParkedSession will reuse it. But if krang is
	// actually still running (e.g. in a different tmux client), block.
	if currentSession != krangSession && !tmux.SessionExists(krangSession) && tmux.SessionExists(parkedSession) {
		if hooks.InstanceIsLive(stateFilePath) {
			return fmt.Errorf("krang is already running for this directory (parked session found: %s)", parkedSession)
		}
		// Stale parked session — not an error, will be reused.
	}

	if currentSession != krangSession {
		if err := tmux.RenameSession(currentSession, krangSession); err != nil {
			return fmt.Errorf("renaming session: %w", err)
		}
	}

	if krangWindowID, err := tmux.CurrentWindowID(); err == nil {
		_ = tmux.RenameWindow(krangWindowID, "🧠")
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
	workspaceRepoStore := db.NewWorkspaceRepoStore(database)

	repoSets, err := workspace.Load(cwd)
	if err != nil {
		repoSets = nil
	}
	repoSets.ApplyUserDefaults(cfg.DefaultVCS, cfg.GitHubOrgs)
	manager := task.NewManager(taskStore, eventStore, workspaceRepoStore, krangSession, parkedSession, cfg.Sandboxes, cfg.DefaultSandbox, stateFilePath, cwd, repoSets)

	if err := manager.Reconcile(); err != nil {
		return fmt.Errorf("initial reconciliation: %w", err)
	}

	hookEvents := make(chan hooks.HookEvent, 64)
	// Workspace mutations requested over HTTP are serialized by the
	// TUI process. The buffer only smooths bursts — the model runs one
	// request at a time regardless.
	workspaceRequests := make(chan hooks.WorkspaceRequest, 16)
	hookServer := hooks.NewServer(stateFilePath, func(event hooks.HookEvent) {
		hookEvents <- event
	}, workspaceRequests)
	if err := hookServer.Start(); err != nil {
		return fmt.Errorf("starting hook server: %w", err)
	}
	defer hookServer.Stop()

	summaryPipeline := summary.NewPipeline(taskStore)

	themeName := cfg.Theme
	if themeName == "" {
		themeName = tui.DefaultThemeName
	}
	theme, err := tui.ResolveTheme(themeName)
	if err != nil {
		return err
	}
	styles := tui.BuildStyles(theme)

	model := tui.NewModel(manager, taskStore, eventStore, workspaceRepoStore, repoSets, hookEvents, workspaceRequests, summaryPipeline, krangSession, parkedSession, cfg, styles)
	program := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}

	// Clean up the parked session if no tasks are parked. This
	// prevents stale sessions from lingering after krang exits.
	if tasks, err := taskStore.List(); err == nil {
		hasParked := false
		for _, t := range tasks {
			if t.State == db.StateParked {
				hasParked = true
				break
			}
		}
		if !hasParked {
			_ = tmux.KillSession(parkedSession)
		}
	}

	return nil
}
