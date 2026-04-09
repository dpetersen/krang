# Krang

A tmux-native workspace manager for Claude Code.

> **Warning:** This project was written almost entirely by Claude Code
> without significant human review of the implementation. It works
> well for the author, but use at your own risk.

## Philosophy

Tmux and Claude Code are good together. Krang doesn't change that
experience — you still interact with Claude in a terminal, you can
still split panes and create windows however you like. Krang just
makes it easier to manage many Claude sessions at once without
losing track of what's happening.

Your tmux workflow is yours. Krang manages its own windows and
leaves everything else alone.

## Features

- **Dashboard** — a single TUI showing all your Claude tasks with
  live status: working, waiting for input, blocked on permissions,
  or done. Activity sparklines show what Claude has been up to at
  a glance.

- **Park and freeze** — park tasks to a background session when
  you need focus, or freeze them to free resources entirely. Resume
  later with full conversation history.

- **Attention sorting** — sort by priority to see which tasks need
  you right now. Krang classifies Claude's output to distinguish
  "done" from "waiting for your input."

- **Workspaces** — optional per-task isolated directories backed by
  git worktrees or jj workspaces. Run experiments across multiple
  repos without polluting your main working trees.

- **Forking** — fork a running task to branch an experiment. The
  fork gets its own workspace and conversation, starting from where
  the original left off.

- **Companion windows** — open linked terminal windows next to any
  task for running tests, tailing logs, or anything else. Companions
  follow their parent through park/unpark.

- **Sandboxing** — named sandbox profiles restrict what each Claude
  session can access. Different profiles for different trust levels.

- **Non-invasive** — krang only manages windows it creates. Your
  own tmux panes, windows, and splits are unaffected. Create
  whatever you want in a krang-managed session and it stays put.

## Prerequisites

- tmux
- Claude Code CLI (`claude`)

## Installation

### Homebrew

```
brew tap dpetersen/tap
brew install krang
```

### From Source

Requires Go 1.26+:

```
go install github.com/dpetersen/krang@latest
```

### First Run

```
krang setup   # installs hooks into ~/.claude/settings.json
krang         # launch the TUI (must be inside tmux)
```

`krang setup` writes a relay script to `~/.config/krang/hooks/relay.sh`
and adds hook entries to `~/.claude/settings.json` so Claude Code
reports events back to krang. It will show you exactly what it plans
to change and ask for confirmation before writing anything.

## How It Works

Krang runs as a Bubble Tea TUI inside a tmux window. When you create a
task, krang opens a new tmux window running Claude Code with a unique
session ID. Claude Code hooks relay events back to krang's HTTP server,
giving you live visibility into each task's status: whether Claude is
working, waiting for input, or blocked on a permission prompt.

### Task Lifecycle

| State | Description |
|-------|-------------|
| Active | tmux window in krang's session, Claude running |
| Parked | window moved to a background tmux session, still running |
| Frozen | no tmux window; session ID saved, can be resumed later |
| Completed | terminal state |
| Failed | terminal state |

### Task Flags

Flags can be set at creation time or toggled on a running task (some
require a relaunch to take effect):

| Flag | Effect |
|------|--------|
| No Sandbox | Launch claude directly, skipping the sandbox wrapper |
| Skip Permissions | Pass `--dangerously-skip-permissions` to claude |
| Debug | Export `KRANG_DEBUG=1` for hook relay logging (see Debugging) |

## Workspaces

Workspaces give each task its own isolated directory with VCS-linked
copies of repos. There are three levels of adoption:

### Level 1: CWD Picker (no config needed)

Without a `krang.yaml`, task creation shows a directory picker
listing immediate subdirectories of krang's working directory.
Select `.` to use the current directory or pick a subdirectory.
The picker is skipped when no subdirectories exist.

### Level 2: Single Repo Workspaces

Create a `krang.yaml` in your working directory:

```yaml
workspace_strategy: single_repo
```

Now task creation shows a repo picker. Each task gets its own clone
under the workspaces directory:

```
~/code/project/
├── repos/              # your source repos
│   └── my-service/
├── workspaces/         # krang creates these
│   └── fix-auth/       # direct clone of my-service
└── krang.yaml
```

### Level 3: Multi-Repo Workspaces

```yaml
workspace_strategy: multi_repo
```

Task creation shows a multi-select repo picker. Each task gets a
directory containing clones of the selected repos:

```
~/code/project/
├── repos/
│   ├── api-server/
│   └── web-app/
├── workspaces/
│   └── auth-refactor/
│       ├── api-server/
│       └── web-app/
└── krang.yaml
```

You can add more repos to a task after launch by editing it from
the detail modal.

### Repo Sets (optional, multi_repo only)

Group repos into named sets for quick selection:

```yaml
workspace_strategy: multi_repo

sets:
  backend:
    - api-server
    - web-app
  terraform:
    - terraform-config
    - terraform-modules
```

The repo picker shows sets as toggle-able headers — toggling a set
selects all its members. Individual repos can still be toggled
independently.

### VCS Behaviors

For existing local repos, krang auto-detects whether each one uses
jj or git by looking for `.jj/` or `.git`. It always uses whichever
VCS the repo already has, regardless of your `default_vcs` setting.

The `default_vcs` setting (in `config.yaml` or `krang.yaml`) only
controls which VCS is used when cloning new repos from GitHub via
the Remote tab. Once a repo is cloned, krang uses whatever is on
disk.

Both VCS backends create lightweight linked working copies that
share the source repo's object store. Before creating a workspace,
krang fetches from origin and branches from the remote default
branch so new worktrees start from the latest upstream state.

#### jj Repos

**Creation:** `jj workspace add` creates a linked working copy in
the workspace directory. The workspace name matches the task name.

**Cleanup:** `jj workspace forget` deregisters the workspace from
the source repo, then the directory is removed. jj workspaces don't
create branches, so there's nothing else to clean up.

**Forking:** `jj duplicate` creates an independent copy of the
current commit, then `jj workspace add` + `jj edit` points the
fork at the duplicate. The source and fork are sibling commits
with no rebase interaction.

#### git Repos

**Creation:** `git worktree add -b krang/<task-name>` creates a
worktree with a branch namespaced under `krang/` so it's clearly
identifiable as krang-managed. The worktree shares the source
repo's object store — no file copying, nearly instant.

**Cleanup on task completion:**

1. `git worktree remove` deregisters the worktree from the source
   repo.
2. `git branch -d krang/<task-name>` deletes the branch. The
   lowercase `-d` is intentional — git refuses to delete branches
   that have commits not present on any remote. If the branch has
   unpushed commits, it's preserved in the local source repo as a
   safety net.
3. The workspace directory is removed.

The completion confirmation modal warns about both conditions
before you confirm:

- **Uncommitted changes** — modified, staged, or untracked files
  in the worktree that will be lost when the directory is deleted.
- **Unpushed commits** — commits on the `krang/<task-name>` branch
  that don't exist on any remote-tracking branch. The branch will
  be preserved in the local source repo so the work isn't lost.
  You can find surviving branches with
  `git branch | grep krang/`.

**Forking:** Creates a new worktree at the source's current HEAD,
then copies the working tree state (including uncommitted and
untracked files) into the fork. The fork gets its own
`krang/<fork-name>` branch.

**Crash recovery:** If krang exits without cleaning up, stale
worktree entries and branches may be left behind. On the next
workspace creation for a task with the same name, krang
automatically prunes stale worktree entries and cleans up the
old branch. You can also clean up manually:

```
cd repos/my-repo
git worktree prune
git branch -D krang/stale-task-name
```

### .worktreeinclude

Git worktrees start as clean checkouts — gitignored files like
`.env` aren't present. To automatically copy specific gitignored
files into new worktrees, create a `.worktreeinclude` file in
your source repo root using gitignore-style patterns:

```
.env
.env.local
config/secrets.json
```

This matches the behavior of Claude Code's built-in
`.worktreeinclude` support.

### Workspace Lifecycle

| Action | What happens |
|--------|-------------|
| Create | Workspace directory created, repos cloned/linked |
| Park | No change (workspace preserved) |
| Freeze | No change (workspace preserved for resume) |
| Complete | Claude stopped, VCS cleanup, directory removed |
| Fork (independent) | New workspace with copied working tree state |
| Fork (shared) | New task in same workspace directory |

If you're using sandboxing with workspaces, additional filesystem
access is needed for VCS operations and config file walking. See
[docs/design/sandboxing.md](docs/design/sandboxing.md#workspace-setup) for details.

## Configuration

Krang works without any configuration. All config is optional.

### config.yaml

User-level config at `~/.config/krang/config.yaml`:

```yaml
# UI theme. Available: catppuccin-mocha (default), catppuccin-latte,
# catppuccin-frappe, catppuccin-macchiato, classic.
theme: catppuccin-mocha

# VCS used when cloning remote repos from the Remote tab.
# "git" (default) or "jj". For existing local repos, krang
# auto-detects from .jj/ or .git/ on disk.
default_vcs: jj

# GitHub orgs for the Remote tab in the repo picker. Merged with
# orgs from the project-level krang.yaml.
github_orgs:
  - myorg

# Named sandbox profiles. See docs/design/sandboxing.md for details.
sandboxes:
  default:
    type: command
    command: "safehouse --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG"

# Sandbox profile to use by default when creating tasks.
default_sandbox: default

# Tmux window color based on attention state. Default: false.
window_colors_enabled: true

# Colors for attention-colored windows. Any tmux color name.
window_color_permission: red
window_color_waiting: yellow
```

### krang.yaml

Project-level config at the root of your working directory. Only
needed if you want workspaces (see [Workspaces](#workspaces)):

```yaml
# Required to enable workspaces. Without this, krang uses the CWD
# picker regardless of other settings.
workspace_strategy: single_repo  # or multi_repo

# Directory containing source repos (relative to krang.yaml).
# Default: "repos"
repos_dir: repos

# Directory where workspaces are created (relative to krang.yaml).
# Default: "workspaces"
workspaces_dir: workspaces

# Default VCS for repos without .jj/ directory. "git" or "jj".
# Overrides config.yaml's default_vcs for this project.
default_vcs: jj

# GitHub orgs for the Remote tab. Merged with config.yaml orgs.
github_orgs:
  - myorg

# Named groups of repos (multi_repo only). Optional.
sets:
  backend:
    - api-server
    - web-app
```

## File Locations

Krang follows XDG conventions for its files. `krang setup` creates
the first two; the rest are managed automatically at runtime.

| Path | Created by | Purpose |
|------|-----------|---------|
| `~/.config/krang/hooks/relay.sh` | `krang setup` | Relay script that forwards Claude hook events to krang |
| `~/.claude/settings.json` | `krang setup` | Hook entries added to Claude Code's config (shared with other hooks) |
| `~/.config/krang/config.yaml` | You (optional) | User configuration |
| `~/.local/share/krang/instances/<dir>/krang.db` | krang | Per-instance SQLite database |
| `~/.local/state/krang/instances/<dir>/krang-state.json` | krang | Per-instance port file (ephemeral, exists while running) |

## Keybindings

Every available action is shown on screen — there are no hidden
keybindings. The hint bars at the bottom of each screen show what
you can do in the current context. Press `?` for a full help
overlay.

## Debugging

Enable the **Debug** flag on a task to set `KRANG_DEBUG=1` in its
environment. This causes the relay script to log every hook event to
`/tmp/krang-debug.log`, including the full JSON payload and HTTP
response status.

```
tail -f /tmp/krang-debug.log
```

This is useful for diagnosing:

- Whether Claude is firing expected hook events (e.g., `PermissionRequest`)
- Whether the relay script can read the state file (sandbox issues)
- Whether events are reaching krang's HTTP server

The debug flag requires a relaunch to take effect (freeze + thaw, or
use the relaunch keybinding).

## Themes

Available themes: `catppuccin-mocha` (default), `catppuccin-latte`,
`catppuccin-frappe`, `catppuccin-macchiato`, `classic`.

Set via the `"theme"` field in config.yaml.

## Development

### Building Locally

Install [mise](https://mise.jdx.dev/) for the dev build tasks:

```
mise run build     # build binary with version from git tags
mise run install   # build and install to ~/.local/bin (override with KRANG_INSTALL_DIR)
mise run test      # run tests
mise run setup     # install hooks (uses dev config)
mise run run       # build, install hooks, launch TUI (uses dev DB)
```

Development uses `KRANG_DB=.krang-dev.db` and
`KRANG_CONFIG=.krang-dev-config.yaml` to isolate from production
paths.

Or build directly with Go:

```
go build -o krang .
```

### Cutting a Release

Krang uses [jj](https://jj-vcs.github.io/jj/) for version control.
Releases are distributed via a Homebrew tap at
[dpetersen/homebrew-tap](https://github.com/dpetersen/homebrew-tap).

1. Tag the release commit:

   ```
   jj tag set v0.1.0-alpha.2
   jj git push
   ```

2. Get the SHA-256 of the GitHub tarball:

   ```
   curl -sL https://github.com/dpetersen/krang/archive/refs/tags/v0.1.0-alpha.2.tar.gz | shasum -a 256
   ```

3. Update `Formula/krang.rb` in the
   [homebrew-tap](https://github.com/dpetersen/homebrew-tap) repo
   with the new tag URL and SHA-256, then push.
