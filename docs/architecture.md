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

## tmux Topology

Krang runs in the user's existing tmux session. It does NOT create a separate session for active tasks.

```
user's tmux session (e.g., "0")
  ├── window 0: "krang" (TUI dashboard)
  ├── window 1: "K!auth-refactor" (@14)     ← managed by krang
  ├── window 2: "K!fix-test" (@15)          ← managed by krang
  ├── window 3: (user's own terminal)       ← NOT touched by krang
  └── window 4: "KF!auth-refactor" (@20)    ← companion window

krang-parked (detached session, holding area)
  └── window 1: "K!update-deps" (@17)       ← parked task
```

**Window ownership:** Krang only manages windows with the `K!` prefix. It never touches, renames, moves, or closes windows without that prefix. Users can freely open ad-hoc terminals.

**Companion windows:** Windows named `KF!<task-name>` are associated with a task. They travel with the task on park/unpark and are killed on freeze. The user creates these manually by naming a tmux window with the convention.

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
| Active | Window in user's session | Running | N/A |
| Parked | Window in `krang-parked` | Running | N/A |
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

Claude Code hooks POST JSON to `http://127.0.0.1:19283/hooks/event`. The hook server runs alongside the TUI in the same process.

**Task correlation:** Krang pre-assigns a UUID via `claude --session-id <uuid>` when creating tasks. Hook payloads include `session_id` which matches.

**Session adoption:** When a frozen task is thawed, `--resume` may assign a new session ID. Krang detects this: when a `SessionStart` arrives with an unknown session ID, it matches to an active task by cwd and updates the stored session ID.

**Hook installation:** `krang setup` merges HTTP hook entries into `~/.claude/settings.json` idempotently, preserving existing user hooks. `krang teardown` removes only krang-owned entries (identified by the krang URL).

## Graceful Shutdown

When completing, killing, or freezing a task:

1. Update DB state FIRST (prevents reconcile race)
2. Find Claude's PID via `tmux display-message #{pane_pid}` → `pgrep -P <shell_pid>`
3. Send SIGINT (same as Ctrl+C — Claude handles this)
4. Wait up to 5 seconds for the window to close
5. Fall back to `tmux kill-window` if it doesn't

State is updated before windows are killed so the 10-second reconciliation tick doesn't race and incorrectly transition the task.

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

## Data Model

SQLite at `~/.config/krang/krang.db` (override with `KRANG_DB` env var). WAL mode for concurrent access.

**tasks table:** id (ULID), name, prompt, state, attention, session_id, cwd, tmux_window, summary, summary_hash, transcript_path, created_at, updated_at

**events table:** id, task_id (FK), event_type, payload (JSON), created_at

## Import

Import existing Claude sessions by name + session ID. Krang auto-discovers the working directory by searching `~/.claude/projects/` for the session file. The encoded project directory name (Claude replaces non-alphanumeric chars with `-`) is decoded by walking the filesystem to resolve ambiguous hyphens in path names.

## CWD Tracking

Task cwd updates live from hook event `cwd` field, which reflects Claude's current working directory (not just the launch directory). Displayed as relative paths when under krang's working directory.

## Development

- `KRANG_DB=.krang-dev.db` isolates the dev database (set in mise.toml)
- Only one krang instance can run at a time (port 19283 + shared DB)
- Uses `jj` for version control, never `git` commands
- Temp files use `NOCOMMIT-` prefix to avoid jj snapshotting them into commits
- Claude spawned via `safehouse claude` wrapper for sandboxing
