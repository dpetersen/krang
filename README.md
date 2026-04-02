# Krang

TUI task orchestrator for managing multiple Claude Code sessions via tmux.

Krang gives you a single dashboard to create, monitor, park, freeze, and
resume Claude Code tasks. Each task runs in its own tmux window, and krang
tracks their state via Claude Code hooks. You can run multiple krang
instances simultaneously for different working directories.

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

## Configuration

Config lives at `~/.config/krang/config.yaml`:

```yaml
sandboxes:
  default:
    type: command
    command: safehouse --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG
  cloud-tools:
    type: command
    command: safehouse --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG --env-pass AWS_PROFILE
default_sandbox: default
theme: catppuccin-mocha
default_vcs: jj
github_orgs:
  - myorg
window_colors_enabled: true
window_color_permission: red
window_color_waiting: yellow
```

| Field | Description |
|-------|-------------|
| `sandboxes` | Named sandbox profiles, each with a `type` and type-specific fields |
| `default_sandbox` | Name of the sandbox profile to use by default |
| `theme` | UI theme (see Themes section) |
| `default_vcs` | Default VCS for remote clones: `"git"` (default) or `"jj"`. Overridden by per-repo config or `.jj/` auto-detection |
| `github_orgs` | GitHub orgs for the Remote tab in the repo picker. Merged with `krang.yaml` orgs |
| `window_colors_enabled` | Enable tmux window color based on attention state |
| `window_color_permission` | Color for permission-blocked windows |
| `window_color_waiting` | Color for waiting windows |

### Sandbox Setup

Krang supports named sandbox profiles configured via `sandboxes` in
config.yaml. Each profile has a `type` field (currently only `command`
is supported) and type-specific fields. Tasks can be assigned a
specific sandbox profile at creation time or changed later via the
flag edit form. Krang itself runs unsandboxed.

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

```yaml
sandboxes:
  default:
    type: command
    command: safehouse --append-profile ~/.config/safehouse/claude-overrides.sb --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG
default_sandbox: default
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

# Default VCS for repos without .jj/ directory. "git" or "jj".
# Overrides config.yaml's default_vcs for this project.
default_vcs: jj

# GitHub orgs for the Remote tab. Merged with config.yaml orgs.
github_orgs:
  - myorg

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

### VCS Behaviors

Krang auto-detects whether each repo uses jj or git (by looking
for `.jj/` or `.git`) and uses the appropriate workspace strategy.
Both create lightweight linked working copies that share the source
repo's object store.

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

### Sandbox Template Variables

Sandbox profiles of type `command` support Go template variables in
the `command` field for granting workspace tasks access to metarepo-level
config files:

| Variable | Description |
|----------|-------------|
| `{{.KrangDir}}` | Krang's working directory (metarepo root) |
| `{{.TaskCwd}}` | Task's original launch cwd (does not drift) |
| `{{.TaskName}}` | Task name |
| `{{.ReposDir}}` | Absolute path to repos directory (empty if no krang.yaml) |

Example — grant config reads and VCS write access for jj/worktree repos:

```yaml
sandboxes:
  default:
    type: command
    command: safehouse --add-dirs-ro={{.KrangDir}}/.mcp.json:{{.KrangDir}}/CLAUDE.md:{{.KrangDir}}/.claude --add-dirs={{.ReposDir}} --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG
default_sandbox: default
```

The `{{.ReposDir}}` write access is needed because both jj workspaces
and git worktrees reference the source repo's object store. See
[docs/workspaces.md](docs/workspaces.md#sandbox-integration) for
details.

## File Locations

| Path | Purpose |
|------|---------|
| `~/.config/krang/config.yaml` | Sandbox, theme, window colors, default VCS, GitHub orgs |
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
| `T` | Cycle sparkline window (1m / 10m / 60m) |
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

Set via the `"theme"` field in config.yaml.

## Development

### Building Locally

Install [mise](https://mise.jdx.dev/) for the dev build tasks:

```
mise run build   # build binary with version from git tags
mise run test    # run tests
mise run setup   # install hooks (uses dev config)
mise run run     # build, install hooks, launch TUI (uses dev DB)
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
