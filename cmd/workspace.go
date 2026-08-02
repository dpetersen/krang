package cmd

import (
	"fmt"

	"github.com/dpetersen/krang/internal/hooks"
	"github.com/dpetersen/krang/internal/workspaceclient"
	"github.com/spf13/cobra"
)

// `krang workspace …` is a sibling of setup and teardown rather than
// anything the TUI runs: it is what a Claude session inside a task
// window calls to change its own workspace. It never touches a
// workspace itself — it posts to the running instance's loopback API and
// lets the Bubble Tea process, the single writer, do the work.
//
// Everything below is a binding. Finding the instance, calling it,
// rendering the answer and choosing the exit code all live in
// internal/workspaceclient, where they are tested without a terminal.

// exitCodeError carries a workspace exit code out through cobra's error
// return. Execute recognises the ExitCode method and exits with it
// silently, because the runner has already written a better message to
// stderr than "Error: exit status 2" would be.
type exitCodeError struct{ code int }

func (e exitCodeError) Error() string { return fmt.Sprintf("workspace command exited %d", e.code) }

func (e exitCodeError) ExitCode() int { return e.code }

const taskSelectionHelp = `Which task:
  --cwd defaults to this process's working directory, so the command works
  with no arguments at all from inside a task's workspace. krang matches it
  against live tasks' workspace directories (longest match, symlinks
  resolved) and echoes back the name it resolved to.
  --task <name> overrides --cwd, and is what you need from outside the
  workspace.`

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Inspect and change a task's workspace from inside it",
	Long: `Inspect and change a krang task's workspace.

These subcommands are for an agent working inside a krang task window. They
talk to the running krang instance over the loopback HTTP API whose port is
in the state file named by $KRANG_STATEFILE, which krang sets for every
session it launches. The TUI process performs the change — it is the single
writer of workspace state — so mutations queue behind each other and behind
whatever the human is doing in the TUI.

  krang workspace list      what working copies this task holds
  krang workspace repos     what the metarepo makes available
  krang workspace add       give this task another working copy
  krang workspace remove    take one back out

Every subcommand accepts --json, which prints krang's response envelope
verbatim, and every subcommand uses the same exit codes. See the individual
--help output for each.`,
}

func init() {
	workspaceCmd.AddCommand(newWorkspaceListCmd())
	workspaceCmd.AddCommand(newWorkspaceReposCmd())
	workspaceCmd.AddCommand(newWorkspaceAddCmd())
	workspaceCmd.AddCommand(newWorkspaceRemoveCmd())
	rootCmd.AddCommand(workspaceCmd)
}

func newWorkspaceListCmd() *cobra.Command {
	var params workspaceclient.Params
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the working copies in a task's workspace",
		Long: `List every working copy (slot) in a task's workspace.

Endpoint: GET /api/workspace. Read-only, so it skips krang's mutation queue
and answers immediately even while the TUI has a modal open. The tradeoff is
that it can catch a workspace mid-clone, which shows up as STATE unrecorded.

` + taskSelectionHelp + `

Output:
  A table of DIR, REPO, SLOT, VCS, BASE, STATE — one row per working copy.
  SLOT is the label, "-" for the task's initial working copy of a repo.
  STATE is "ok"; "missing" (krang recorded it but the directory is gone);
  or "unrecorded" (a directory krang did not create, so its repo and label
  are guessed from its name and only best-effort).
  --json prints the raw envelope, including slots[] with every key present.

` + workspaceclient.ExitCodeHelp,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkspace(cmd, hooks.WorkspaceOpList, params, asJSON)
		},
	}
	addTaskFlags(cmd, &params, &asJSON)
	return cmd
}

func newWorkspaceReposCmd() *cobra.Command {
	var params workspaceclient.Params
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "repos",
		Short: "List the repos the metarepo makes available",
		Long: `List every repo krang can add to a workspace, and which of them this task
already holds.

Endpoint: GET /api/workspace/repos. Read-only, so it skips the mutation queue.
Only repos actually cloned into the metarepo's repos dir are listed — a repo
named in a krang.yaml set but never cloned is not there to add.

` + taskSelectionHelp + `

Output:
  A table of REPO, IN-TASK, SETS. IN-TASK is yes when the task holds at least
  one working copy of that repo; a second working copy of a repo does not
  make it a second repo.
  --json prints the raw envelope, including repos[] of {name, in_task, sets}.

` + workspaceclient.ExitCodeHelp,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkspace(cmd, hooks.WorkspaceOpRepos, params, asJSON)
		},
	}
	addTaskFlags(cmd, &params, &asJSON)
	return cmd
}

func newWorkspaceAddCmd() *cobra.Command {
	var params workspaceclient.Params
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "add --repo <repo>",
		Short: "Add a working copy of a repo to a task's workspace",
		Long: `Add one working copy of a repo to a task's workspace.

One verb covers both "a repo this task doesn't have" and "another checkout of
one it does" — which it turns out to be follows from what the workspace holds,
not from what you asked for. A repo the task already holds needs an explicit
--label: krang refuses with reason label_required (exit 2) and suggests a free
one rather than handing you a numbered checkout you can't tell apart.

Endpoint: POST /api/workspace/add. This is a mutation, so it queues behind any
other workspace change including the human's, and it does a real clone — allow
it up to a minute.

Flags:
  --repo   Required. A name from "krang workspace repos".
  --label  Names a second (or third) working copy of a repo the task already
           holds. Lowercase alphanumerics and single dashes; "--" is reserved.
           Omit it for a repo the task does not hold yet.
  --base   Revset (jj) or commit-ish (git) the new working copy starts from.
           Default: the repo's detected remote default branch. Whatever is
           used is what gets recorded as the working copy's base.

` + taskSelectionHelp + `

Output:
  The new working copy's absolute path on stdout, and nothing else, so it can
  be used directly. --json prints the raw envelope instead; data.path is the
  same value and slot describes the working copy.

Refusals worth knowing (all exit 2, nothing applied):
  label_required    the task already holds this repo; pass --label.
  slot_limit        the task is at the per-task cap; the message names what
                    could be removed to make room.
  shared_workspace  two tasks share this workspace, so nothing says who would
                    own the new working copy. Fork independently instead.

` + workspaceclient.ExitCodeHelp,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkspace(cmd, hooks.WorkspaceOpAdd, params, asJSON)
		},
	}
	cmd.Flags().StringVar(&params.Repo, "repo", "", "repo to add (required); see \"krang workspace repos\"")
	cmd.Flags().StringVar(&params.Label, "label", "", "label for a second working copy of a repo the task already holds")
	cmd.Flags().StringVar(&params.Base, "base", "", "revset (jj) or commit-ish (git) to start from (default: remote default branch)")
	addTaskFlags(cmd, &params, &asJSON)
	return cmd
}

func newWorkspaceRemoveCmd() *cobra.Command {
	var params workspaceclient.Params
	var asJSON bool

	cmd := &cobra.Command{
		Use:     "remove (--dir <dir> | --repo <repo> [--label <label>])",
		Aliases: []string{"rm"},
		Short:   "Remove one working copy from a task's workspace",
		Long: `Remove one working copy from a task's workspace.

krang forgets the VCS identity it recorded for the working copy, removes the
directory, and drops the provenance row — in that order, stopping at the first
failure so all three stay in step and the identical command can be retried.

Removing a repo's last working copy is not special-cased: it is how a repo
leaves a task, through the same gates. The response says whether the repo was
dropped entirely.

Endpoint: DELETE /api/workspace/slot. This is a mutation, so it queues behind
any other workspace change including the human's.

Naming the working copy — one of:
  --dir <dir>              Exact, and what "krang workspace list" reports.
  --repo <repo> [--label]  Friendlier, and must match exactly one working
                           copy; krang refuses with ambiguous_slot otherwise.

Flags:
  --force  Waives the unsaved-work refusal (uncommitted changes or unpushed
           commits) and the "recorded but not on disk" refusal. It cannot
           override workspace_root: in a single_repo task the only checkout IS
           the task's working directory, so complete the task instead.

` + taskSelectionHelp + `

Output:
  One line naming what was removed. --json prints the raw envelope, whose
  slot describes the removed working copy and data.repo_dropped says whether
  the task still holds any working copy of that repo.

Refusals worth knowing (all exit 2, nothing applied):
  unsaved_work    removal would destroy work. blockers[] in the JSON envelope
                  says exactly what. Push or commit, or pass --force.
  slot_missing    recorded but not on disk. --force forgets it anyway.
  ambiguous_slot  --repo matched more than one working copy; use --dir.

` + workspaceclient.ExitCodeHelp,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkspace(cmd, hooks.WorkspaceOpRemoveSlot, params, asJSON)
		},
	}
	cmd.Flags().StringVar(&params.Dir, "dir", "", "working copy directory as reported by \"krang workspace list\"")
	cmd.Flags().StringVar(&params.Repo, "repo", "", "repo whose working copy to remove (with --label when the task holds more than one)")
	cmd.Flags().StringVar(&params.Label, "label", "", "slot label, paired with --repo")
	cmd.Flags().BoolVar(&params.Force, "force", false, "remove even if it destroys uncommitted or unpushed work")
	addTaskFlags(cmd, &params, &asJSON)
	return cmd
}

// addTaskFlags gives every subcommand the same three flags in its own
// Flags section rather than inheriting them as persistent flags of the
// group, so that one --help is enough to use one subcommand.
func addTaskFlags(cmd *cobra.Command, params *workspaceclient.Params, asJSON *bool) {
	cmd.Flags().StringVar(&params.Task, "task", "", "task name (overrides --cwd)")
	cmd.Flags().StringVar(&params.Cwd, "cwd", "", "directory inside the task's workspace (default: this process's cwd)")
	cmd.Flags().BoolVar(asJSON, "json", false, "print krang's response envelope verbatim")
}

func runWorkspace(cmd *cobra.Command, op hooks.WorkspaceOp, params workspaceclient.Params, asJSON bool) error {
	runner := workspaceclient.Runner{
		Stdout: cmd.OutOrStdout(),
		Stderr: cmd.ErrOrStderr(),
	}
	code := runner.Run(cmd.Context(), workspaceclient.Request{Op: op, Params: params, JSON: asJSON})
	if code != workspaceclient.ExitOK {
		return exitCodeError{code: code}
	}
	return nil
}
