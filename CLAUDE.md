# Krang

TUI task orchestrator for managing multiple Claude Code sessions via tmux.

## Architecture

- **Go + Bubble Tea** TUI running in a tmux window
- **SQLite** per-instance at `~/.local/share/krang/instances/<encoded-cwd>/krang.db` (override with `KRANG_DB` env var)
- **Claude Code command hooks** via relay script that reads `KRANG_STATEFILE` for the dynamic port
- **AI summaries** via `claude -p --model haiku` with structured JSON output
- Claude spawned via configurable sandbox wrapper

## Multi-Instance Support

Multiple krang instances can run simultaneously for different working directories. Each instance gets:
- Its own dynamic port (bound to `:0`) with state file at `~/.local/state/krang/instances/<encoded-cwd>/krang-state.json`
- Its own SQLite database at `~/.local/share/krang/instances/<encoded-cwd>/krang.db`
- Its own tmux sessions: `k-<instanceID>` (active) and `k-<instanceID>-parked`
- Instance ID format: `<basename>-<4 hex SHA-256 of full path>` (e.g., `krang-496d`)

## File Locations

| Path | Purpose | XDG category |
|------|---------|-------------|
| `~/.config/krang/config.json` | Sandbox command, window colors | Config |
| `~/.config/krang/hooks/relay.sh` | Static relay script (Claude settings.json points here) | Config |
| `~/.local/share/krang/instances/…/krang.db` | Per-instance SQLite database | Data |
| `~/.local/state/krang/instances/…/krang-state.json` | Per-instance port file (ephemeral, exists while running) | State |

## Task States

- **Active** — tmux window in krang's session, Claude running
- **Parked** — tmux window moved to parked session, still running
- **Frozen** (DB: `dormant`) — no tmux window, session ID saved for `--resume`
- **Completed/Failed** — terminal states; names freed for reuse

## Window Naming

- `<name>` — task windows, identified by `@krang-task` tmux user option
- `<name>+` — companion windows, identified by `@krang-companion` tmux user option
- `@krang-attn` option set on task windows with attention state (ok/waiting/permission/error/done) for custom tmux theme integration
- The krang TUI window is named `🧠`

## Key Packages

- `internal/db/` — SQLite schema, task CRUD, event log
- `internal/pathutil/` — instance ID, XDG path helpers, Claude path encoding
- `internal/tmux/` — session/window/pane operations via `tmux` CLI
- `internal/task/` — high-level lifecycle (create, park, freeze, etc.), reconciliation, import, session cwd decoder
- `internal/hooks/` — HTTP server for Claude Code hook events, relay script + settings.json installer
- `internal/summary/` — ANSI stripping, `claude -p` wrapper, summary pipeline
- `internal/proctree/` — process tree walking, noise/age filtering, leaf-only display for background child process awareness
- `internal/workspace/` — `krang.yaml` parsing, workspace creation/destruction, VCS operations (jj workspace add, local git clone)
- `internal/tui/` — Bubble Tea model, view, keybindings, messages, theming

## Theming

Styles are derived from a `Theme` struct with semantic color roles (Title, Error, Active, etc.). The `Styles` struct holds precomputed lipgloss styles built via `BuildStyles(theme)`. Available themes: `classic` (original ANSI 256 colors), `catppuccin-mocha` (default), `catppuccin-latte`, `catppuccin-frappe`, `catppuccin-macchiato`. Set via `"theme"` field in config.json.

## Task Creation

Task creation and import use `charmbracelet/huh` forms (multi-step wizard). The task table uses `bubbles/table`. Task names must match `[a-zA-Z0-9_-]+`.

## Workspaces

Optional per-task isolated directories configured via `krang.yaml` at the metarepo root. See `docs/workspaces.md` for full details.

- **`workspace_strategy: single_repo`** — pick one repo, workspace dir is a direct clone
- **`workspace_strategy: multi_repo`** — pick multiple repos (with optional set grouping via a custom toggle-list component), workspace dir contains clones
- **No strategy** — CWD picker (original behavior)
- Git clones use local hardlinks for speed; jj repos use `jj workspace add`
- Workspaces destroyed on task complete/kill (jj workspace forget + rm -rf)
- `W` keybinding adds repos to existing multi_repo workspaces
- Sandbox command supports Go templates (`{{.KrangDir}}`, `{{.TaskCwd}}`, `{{.TaskName}}`, `{{.ReposDir}}`) for granting sandboxed tasks access to metarepo config files

## Building and Running

```
mise run run     # build, install hooks, launch TUI (uses dev DB)
mise run test    # run tests
mise run build   # build binary only
mise run setup   # install Claude Code hooks only
```

Must be run inside tmux. Uses `jj` for version control, not `git`.

Development uses `KRANG_DB=.krang-dev.db` and `KRANG_CONFIG=.krang-dev-config.json` (set in mise.toml) to isolate from production paths.

## Graceful Shutdown

Tasks are shut down via SIGINT to the Claude process (found via `pgrep -P <shell_pid>`), with a 5-second timeout before falling back to `tmux kill-window`. DB state is updated before killing windows to prevent reconcile races.

On krang exit, parked tasks are offered for freezing. If frozen (or none exist), the parked session is cleaned up automatically.

## Hook Events

Krang listens for: `SessionStart`, `UserPromptSubmit`, `Stop`, `PermissionRequest`, `ToolResult`, `TaskCompleted`, `StopFailure`, `Notification`, `SessionEnd`. Events matched to tasks by `session_id`. Resumed sessions adopted via cwd matching on `SessionStart`.

Hooks are `type: "command"` entries in `~/.claude/settings.json` pointing to the relay script. The relay script only forwards events when `KRANG_STATEFILE` is set (which krang does for sessions it launches), so standalone Claude sessions are unaffected.

## Import

Import discovers the cwd automatically by searching `~/.claude/projects/` for the session ID file. The encoded project directory name is decoded by walking the filesystem to handle ambiguous hyphens in path names.

## CWD Tracking

Task cwd updates live from hook event payloads. Displayed as relative paths when under krang's working directory, tilde-ified otherwise.

## Sort Modes

- **Created** (default) — all tasks, stable creation order
- **Priority** — active tasks only, sorted by attention: permission > error > waiting > ok > done

## Sandboxing

Krang itself runs unsandboxed; only the Claude processes inside task windows are sandboxed. The sandbox must pass through `KRANG_STATEFILE` (required) and `KRANG_DEBUG` (optional) env vars, and allow read access to `~/.local/state/krang/` (state file) and read+execute on `~/.config/krang/hooks/` (relay script). No write access to krang paths is needed from inside the sandbox. See README.md for full details.

## Debugging

The Debug task flag (`KRANG_DEBUG=1`) enables relay script logging to `/tmp/krang-debug.log`. Logs the full JSON payload of each hook event and the HTTP status of delivery to krang. Requires relaunch to take effect.
