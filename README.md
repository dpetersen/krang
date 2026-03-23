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

## File Locations

| Path | Purpose |
|------|---------|
| `~/.config/krang/config.json` | Sandbox command, theme, window colors |
| `~/.config/krang/hooks/relay.sh` | Relay script (written by `krang setup`) |
| `~/.local/share/krang/instances/<dir>/krang.db` | Per-instance SQLite database |
| `~/.local/state/krang/instances/<dir>/krang-state.json` | Per-instance port file (ephemeral) |

## Keybindings

| Key | Action |
|-----|--------|
| `n` | New task |
| `Enter` | Focus task (switch to its tmux window) |
| `p` | Park / unpark task |
| `d` | Freeze task |
| `w` | Wake (thaw) frozen task |
| `f` | Edit task flags |
| `s` | Toggle sort mode (created / priority) |
| `/` | Filter tasks |
| `?` | Help / debug log |
| `q` | Quit |

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
