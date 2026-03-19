# Krang

TUI task orchestrator for managing multiple Claude Code sessions via tmux.

## Architecture

- **Go + Bubble Tea** TUI running in a tmux window
- **SQLite** at `~/.config/krang/krang.db` for task/event storage (override with `KRANG_DB` env var)
- **Claude Code HTTP hooks** on `127.0.0.1:19283` for real-time event ingestion
- **AI summaries** via `claude -p --model haiku` with structured JSON output
- Claude spawned via `safehouse claude` wrapper

## Task States

- **Active** — tmux window in user's current session, Claude running
- **Parked** — tmux window moved to `krang-parked` session, still running
- **Frozen** (DB: `dormant`) — no tmux window, session ID saved for `--resume`
- **Completed/Failed** — terminal states; names freed for reuse

## Window Naming

- `K!<name>` — krang-managed Claude Code task windows
- `KF!<name>` — companion windows associated with a task (travel with the task on park/unpark, killed on freeze)
- Windows without these prefixes are never touched by krang

## Key Packages

- `internal/db/` — SQLite schema, task CRUD, event log
- `internal/tmux/` — session/window/pane operations via `tmux` CLI
- `internal/task/` — high-level lifecycle (create, park, freeze, etc.), reconciliation, import, session cwd decoder
- `internal/hooks/` — HTTP server for Claude Code hook events, settings.json installer
- `internal/summary/` — ANSI stripping, `claude -p` wrapper, summary pipeline
- `internal/tui/` — Bubble Tea model, view, keybindings, messages

## Building and Running

```
mise run run     # build, install hooks, launch TUI (uses dev DB)
mise run test    # run tests
mise run build   # build binary only
mise run setup   # install Claude Code hooks only
```

Must be run inside tmux. Uses `jj` for version control, not `git`.

Development uses `KRANG_DB=.krang-dev.db` (set in mise.toml) to isolate from the production database.

## Graceful Shutdown

Tasks are shut down via SIGINT to the Claude process (found via `pgrep -P <shell_pid>`), with a 5-second timeout before falling back to `tmux kill-window`. DB state is updated before killing windows to prevent reconcile races.

## Hook Events

Krang listens for: `SessionStart`, `UserPromptSubmit`, `Stop`, `PermissionRequest`, `TaskCompleted`, `StopFailure`, `Notification`, `SessionEnd`. Events matched to tasks by `session_id`. Resumed sessions adopted via cwd matching on `SessionStart`.

## Import

Import discovers the cwd automatically by searching `~/.claude/projects/` for the session ID file. The encoded project directory name is decoded by walking the filesystem to handle ambiguous hyphens in path names.

## CWD Tracking

Task cwd updates live from hook event payloads. Displayed as relative paths when under krang's working directory, tilde-ified otherwise.

## Sort Modes

- **Created** (default) — all tasks, stable creation order
- **Priority** — active tasks only, sorted by attention: permission > error > waiting > ok > done
