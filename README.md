# Krang

TUI task orchestrator for managing multiple Claude Code sessions via tmux.

Krang gives you a single dashboard to create, monitor, park, freeze, and
resume Claude Code tasks. Each task runs in its own tmux window, and krang
tracks their state via Claude Code hooks. You can run multiple krang
instances simultaneously for different working directories.

## Prerequisites

- Go 1.26+
- tmux
- [mise](https://mise.jdx.dev/) (for build tasks)
- Claude Code CLI (`claude`)

## Quick Start

```
go build -o krang .
./krang setup   # installs hooks into ~/.claude/settings.json
./krang         # launch the TUI (must be inside tmux)
```

Or with mise:

```
mise run run    # build, install hooks, launch TUI
```

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

## Configuration

Config lives at `~/.config/krang/config.json`:

```json
{
  "sandbox_command": "safehouse --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG",
  "theme": "catppuccin-mocha",
  "window_colors_enabled": true,
  "window_color_permission": "red",
  "window_color_waiting": "yellow"
}
```

### Sandbox Setup

Krang supports wrapping Claude in a sandbox (configured via
`sandbox_command`). The sandbox runs around the Claude process inside
each task's tmux window. Krang itself runs unsandboxed.

If you use a sandbox, it must allow the following or hook events will
silently fail:

**Environment variables to pass through:**

| Variable | Purpose |
|----------|---------|
| `KRANG_STATEFILE` | Required. Path to the port file the relay script reads |
| `KRANG_DEBUG` | Optional. Enables relay script debug logging |

**Filesystem access the sandboxed process needs:**

| Path | Access | Purpose |
|------|--------|---------|
| `~/.local/state/krang/` | Read | Relay script reads the state file for the port |
| `~/.config/krang/hooks/` | Read + Execute | Relay script lives here |

Note: the sandboxed Claude does **not** need access to the SQLite
database (`~/.local/share/krang/`) or write access to any krang paths.
Krang handles all DB writes from outside the sandbox.

**Example safehouse configuration** (`~/.config/safehouse/claude-overrides.sb`):

```scheme
;; Krang: relay script reads state file, executes from hooks dir
(allow file-read* (subpath "~/.local/state/krang"))
(allow file-write* (subpath "~/.config/krang"))
(allow process-exec (subpath "~/.config/krang/hooks"))
```

And in your krang config, pass the env vars through:

```json
{
  "sandbox_command": "safehouse --append-profile ~/.config/safehouse/claude-overrides.sb --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG"
}
```

If hook events aren't showing up, the sandbox is the most likely cause.
See the Debugging section below.

## Workspaces

Workspaces give each task its own isolated directory with VCS-linked
copies of repos. There are three levels of adoption:

### Level 1: CWD Picker (no config needed)

Without a `krang.yaml`, task creation shows a directory picker
listing immediate subdirectories of krang's working directory.
Select `.` for the current directory (original behavior) or pick a
subdirectory. The picker is skipped when no subdirectories exist.

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
│   ├── gonfalon/
│   └── gonfalon-priv/
├── workspaces/
│   └── auth-refactor/
│       ├── gonfalon/
│       └── gonfalon-priv/
└── krang.yaml
```

Press `W` on an active or parked workspace task to add more repos.

### Repo Sets (optional, multi_repo only)

Group repos into named sets for quick selection:

```yaml
workspace_strategy: multi_repo

sets:
  backend:
    - gonfalon
    - gonfalon-priv
  terraform:
    - terraform-config
    - terraform-modules
```

The repo picker shows sets as toggle-able headers — toggling a set
selects all its members. Individual repos can still be toggled
independently.

### krang.yaml Reference

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

# VCS override per repo. Only needed when auto-detection (looks for
# .jj/ directory) gives the wrong answer.
repos:
  some-repo:
    vcs: git  # or jj

# Named groups of repos (multi_repo only). Optional.
sets:
  backend:
    - gonfalon
    - gonfalon-priv
```

### VCS Operations

- **jj repos**: `jj workspace add` — linked working copy, shared
  object store, space-efficient
- **git repos**: local `git clone` — uses hardlinks for the object
  store, nearly instant and space-efficient

### Workspace Lifecycle

Workspaces are created when a task is created and destroyed when a
task is completed or killed. For jj repos, `jj workspace forget` is
called before removal. Frozen tasks keep their workspace intact.

### Sandbox Template Variables

When using a sandbox, the `sandbox_command` supports Go template
variables for granting workspace tasks access to metarepo-level
config files:

| Variable | Description |
|----------|-------------|
| `{{.KrangDir}}` | Krang's working directory (metarepo root) |
| `{{.TaskCwd}}` | Task's original launch cwd (does not drift) |
| `{{.TaskName}}` | Task name |
| `{{.ReposDir}}` | Absolute path to repos directory (empty if no krang.yaml) |

Example — grant config reads and VCS write access for jj/worktree repos:

```json
{
  "sandbox_command": "safehouse --add-dirs-ro={{.KrangDir}}/.mcp.json:{{.KrangDir}}/CLAUDE.md:{{.KrangDir}}/.claude --add-dirs={{.ReposDir}} --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG"
}
```

The `{{.ReposDir}}` write access is needed because jj workspaces and
git worktrees reference the source repo's object store. Plain git
clones are self-contained and don't need it. See
[docs/workspaces.md](docs/workspaces.md#sandbox-integration) for
details.

## File Locations

| Path | Purpose |
|------|---------|
| `~/.config/krang/config.json` | Sandbox command, theme, window colors |
| `~/.config/krang/hooks/relay.sh` | Relay script (written by `krang setup`) |
| `~/.local/share/krang/instances/<dir>/krang.db` | Per-instance SQLite database |
| `~/.local/state/krang/instances/<dir>/krang-state.json` | Per-instance port file (ephemeral) |

## Keybindings

### Global Keys

| Key | Action |
|-----|--------|
| `n` | New task |
| `Enter` | Focus task (switch to its tmux window) |
| `Tab` | Open task detail modal |
| `c` | Complete task (with confirmation) |
| `j/k` | Navigate up/down |
| `s` | Toggle sort mode (created / priority) |
| `/` | Filter tasks |
| `:` | Command palette (sit rep, import, compact) |
| `?` | Help |
| `q` | Quit |

### Detail Modal Keys

| Key | Action |
|-----|--------|
| `p` | Park / unpark |
| `f` | Freeze / unfreeze |
| `c` | Complete task |
| `+` | Create companion window |
| `F` | Edit task flags |
| `W` | Add repos to workspace task |
| `Enter` | Focus task window |
| `Esc/Tab` | Close modal |

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

Set via the `"theme"` field in config.json.
