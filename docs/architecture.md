# Architecture

## Problem

Running multiple Claude Code sessions in tmux creates cognitive overload. The user is the bottleneck — acting as scheduler, state machine, and notification system for coding agents. There's no way to tell which sessions need attention, no way to park work without losing it, and no way to get a quick overview of what's happening across all sessions.

## Solution

Krang is a TUI control plane that manages Claude Code sessions as **tasks** with lifecycle states. tmux remains the execution layer — krang doesn't replace or wrap the Claude terminal experience. It just adds lifecycle management, attention routing, and summarization around it.

## Core Concepts

### Tasks, Not Windows

A task is a unit of work with a Claude Code session. It has a name, a state, an attention indicator, and optionally a tmux window. tmux windows are a visualization of tasks, not the other way around.

### Three Layers

```
┌─────────────────────────────────┐
│  TUI (Bubble Tea)               │  Control plane + dashboard
├─────────────────────────────────┤
│  Task Manager                   │  Lifecycle operations
├─────────────────────────────────┤
│  tmux + SQLite + Hooks          │  Execution, persistence, events
└─────────────────────────────────┘
```

## Multi-Instance Support

Multiple krang instances can run simultaneously for different working directories (e.g., different metarepos). Each instance is identified by `<basename>-<4 hex SHA-256>` of the absolute working directory path.

Per-instance resources:
- **Dynamic port** — hook server binds to `:0`; port written to a state file
- **SQLite database** — persistent task/event storage
- **tmux sessions** — `k-<instanceID>` (active) and `k-<instanceID>-parked`

### File Locations

Krang follows XDG conventions for file placement:

| Path | Purpose | Lifecycle |
|------|---------|-----------|
| `~/.config/krang/config.yaml` | Sandbox command, window colors | Permanent, user-edited |
| `~/.config/krang/hooks/relay.sh` | Static relay script (Claude settings.json points here) | Written by setup, static |
| `~/.local/share/krang/instances/<encoded-cwd>/krang.db` | Per-instance SQLite database | Persistent across restarts |
| `~/.local/state/krang/instances/<encoded-cwd>/krang-state.json` | Per-instance port file | Ephemeral, exists while running |

### Instance Collision Detection

On startup, krang checks for an existing instance:
1. If a tmux session named `k-<instanceID>` already exists, error with attach instructions
2. If a state file exists with a responding `/health` endpoint, error with port info
3. If a state file exists but the port doesn't respond, treat as stale and overwrite

## tmux Topology

Krang renames its tmux session to `k-<instanceID>` on startup for visibility in `tmux ls`.

```
k-myproject-a3f2 (attached, active session)
  ├── window 0: "🧠" (TUI dashboard)
  ├── window 1: "auth-refactor" (@14)       ← task window (@krang-task=auth-refactor)
  ├── window 2: "fix-test" (@15)            ← task window (@krang-task=fix-test)
  ├── window 3: (user's own terminal)       ← NOT touched by krang
  └── window 4: "auth-refactor+" (@20)      ← companion (@krang-companion=auth-refactor)

k-myproject-a3f2-parked (detached, holding area)
  └── window 1: "update-deps" (@17)         ← parked task
```

**Window ownership:** Krang identifies its windows via `@krang-task` and `@krang-companion` tmux user options set at creation time. It never touches windows without these options. Users can freely open ad-hoc terminals.

**Companion windows:** Windows with the `@krang-companion` option are associated with a task. They travel with the task on park/unpark and are killed on freeze. Created via the `+` keybinding in the TUI.

**Window identification:** Krang uses tmux's stable window IDs (`@N`) which survive moves between sessions. It never relies on window indexes, which change.

## Task States

```
             park            freeze
  active ──────────> parked ──────────> frozen (dormant)
    ^                  |                   |
    |     unpark       |                   |
    +──────────────────+                   |
    |                       thaw           |
    +──────────────────────────────────────+
    |
    |  complete / kill
    +──────────────────> completed / failed
```

| State | tmux | Claude | Resumable |
|-------|------|--------|-----------|
| Active | Window in krang's session | Running | N/A |
| Parked | Window in parked session | Running | N/A |
| Frozen | No window | Not running | Yes, via `--resume` |
| Completed | No window | Not running | No |
| Failed | No window | Not running | No |

Names are freed for reuse when a task reaches completed or failed.

## Attention States

Orthogonal to task state. Driven by Claude Code hook events.

| Attention | Meaning | Visual | Hook trigger |
|-----------|---------|--------|-------------|
| ok | Claude is working | dim | `SessionStart`, `UserPromptSubmit` |
| waiting | Needs user input | yellow | `Stop`, `Notification(idle_prompt)` |
| permission | Permission dialog | red bold | `PermissionRequest`, `Notification(permission_prompt)` |
| error | Something broke | red | `StopFailure` |
| done | Task complete | green | `TaskCompleted` |

**Known limitation:** When a permission is denied, Claude returns to the prompt without firing `Stop`. The attention stays at "permission" until the user next interacts with that session (triggering `UserPromptSubmit`).

## Event System

Claude Code hooks are `type: "command"` entries in `~/.claude/settings.json` pointing to a static relay script at `~/.config/krang/hooks/relay.sh`. The relay script reads `KRANG_STATEFILE` (set by krang in each tmux window it creates) to find the current port, then forwards the event via HTTP. Standalone Claude sessions (without `KRANG_STATEFILE`) are unaffected.

The hook server runs alongside the TUI in the same process, bound to a dynamic port on `127.0.0.1`.

**Task correlation:** Krang pre-assigns a UUID via `claude --session-id <uuid>` when creating tasks. Hook payloads include `session_id` which matches.

**Session adoption:** When a frozen task is thawed, `--resume` may assign a new session ID. Krang detects this: when a `SessionStart` arrives with an unknown session ID, it matches to an active task by cwd and updates the stored session ID.

**Hook installation:** `krang setup` writes the relay script and merges command hook entries into `~/.claude/settings.json` idempotently, preserving existing user hooks. It also removes any legacy HTTP-type krang hooks from before multi-instance support. `krang teardown` removes only krang-owned entries (identified by the relay script path).

## Graceful Shutdown

When completing, killing, or freezing a task:

1. Find Claude's PID via `tmux display-message #{pane_pid}` → walk child processes through sandbox wrappers
2. Send SIGINT (same as Ctrl+C — Claude handles this)
3. Wait for Claude to exit, then send Enter to dismiss the shell's "read" prompt
4. Wait up to 15 seconds for the window to close
5. Fall back to `tmux kill-window` if it doesn't
6. Update DB state only after the window is confirmed dead

The window is killed before DB state is updated — if the kill fails, the task stays in its current state and the error is surfaced in both the TUI debug log and the DB events table.

When krang itself exits, it prompts to freeze any remaining parked tasks and cleans up the parked session if empty.

## AI Summaries

Periodic one-liner summaries via `claude -p --model haiku`:

1. `tmux capture-pane` (last 50 lines)
2. Strip ANSI escape codes
3. Hash content — skip if unchanged
4. Call Haiku with structured JSON schema
5. Store result in DB, display in task table

**Trigger:** 30-second auto tick + manual `r` key.
**Rate limit:** One call per task per 30 seconds.
**Cost guard:** Content hashing prevents redundant calls.
**Auth:** Uses `claude -p` which leverages existing Claude Code auth (works with Enterprise OAuth).

## Sit Rep

Full briefing on all active tasks via `claude -p --model sonnet`:

1. Gather metadata + transcript paths for active tasks
2. Claude reads transcripts via the Read tool
3. Generates markdown briefing with per-task status and recommendations
4. Rendered via glamour for styled terminal output
5. Displayed in scrollable full-screen viewport

**Budget:** Capped at $1.00 per sit rep via `--max-budget-usd`.

## Process Tree Awareness

Background child processes (CI watchers, long builds, test runners) are surfaced per task via the `internal/proctree` package. A single `ps -eo pid,ppid,etime,command -ww` call captures the full system process table, then the tree is resolved per task:

1. **Find Claude** — BFS from the tmux shell PID through sandbox wrappers until a process with basename `claude` is found
2. **Collect descendants** — recursive walk of Claude's children
3. **Filter noise** — remove MCP servers (`mcp` substring), `caffeinate`, `pgrep`/`ps`, and `nah`
4. **Filter young** — processes must be alive for 30+ seconds (eliminates ephemeral tool invocations)
5. **Filter ancestors** — remove wrapper processes whose children are also in the result, leaving only leaf processes

**Collection triggers:** 5-second tick + immediate collection on `Stop` hook events (when Claude transitions to idle).

**TUI display:** The attention column shows `wait⚙N` when Claude is stopped but N background children are still running. The indicator is hidden during active work (`ok` attention) to avoid flicker from ephemeral tool use.

**Summary/sit rep integration:** Process lists are passed to both Haiku (per-task summaries) and Sonnet (sit rep) prompts for richer context.

Process data is transient — stored on the TUI model, never persisted to the database.

## Data Model

SQLite per-instance at `~/.local/share/krang/instances/<encoded-cwd>/krang.db` (override with `KRANG_DB` env var). WAL mode for concurrent access.

**tasks table:** id (ULID), name, prompt, state, attention, session_id, cwd, tmux_window, summary, summary_hash, transcript_path, created_at, updated_at

**events table:** id, task_id (FK), event_type, payload (JSON), created_at

## Import

Import existing Claude sessions by name + session ID. Krang auto-discovers the working directory by searching `~/.claude/projects/` for the session file. The encoded project directory name (Claude replaces non-alphanumeric chars with `-`) is decoded by walking the filesystem to resolve ambiguous hyphens in path names.

## CWD Tracking

Task cwd updates live from hook event `cwd` field, which reflects Claude's current working directory (not just the launch directory). Displayed as relative paths when under krang's working directory.

## Development

- `KRANG_DB=.krang-dev.db` and `KRANG_CONFIG=.krang-dev-config.yaml` isolate dev state (set in mise.toml)
- Uses `jj` for version control, never `git` commands
- Temp files use `NOCOMMIT-` prefix to avoid jj snapshotting them into commits
- Claude sandbox wrapper is configurable via `krang setup`
