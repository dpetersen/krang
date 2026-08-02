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

## Workspace Request Serialization

Hook events flow one way: the relay script posts, krang records. Workspace mutations need an answer, and they need to not collide with each other or with what the user is doing in the TUI. Both requirements land on the same fact — the Bubble Tea process is the only writer of workspace state — so requests are funneled through it.

```
POST /api/workspace/…      hook server        Bubble Tea process
  ────────────────────►  decode params  ──►  chan WorkspaceRequest
                         wait (bounded)         │
                                                ▼
                                          FIFO queue ─► one tea.Cmd at a time
                                                │            │
  ◄──── JSON response ◄──── Reply chan ◄────────┴────────────┘
```

**Non-blocking Update.** The model receives requests with a self-re-arming `tea.Cmd`, the same pattern as hook events, and only ever *queues* them in `Update`. A drain step runs after every message and starts the head of the queue when the workspace is free, so a request held behind a modal starts on the first message after the modal closes.

**Reads don't queue.** The queue exists to keep two writers off one workspace. `GET /api/workspace` and `GET /api/workspace/repos` read the `workspace_repos` rows and stat some directories; they take no locks and change nothing, so they start immediately rather than waiting on `workspaceBusy()` — which stays true for as long as a human leaves a modal open. The tradeoff is that a listing can catch a workspace mid-clone: a directory present with no row yet. That reads out as `recorded: false`, which is what it is. Reads still run inside the Bubble Tea process, so there is still exactly one thing reading krang's own view of workspace state, and `workspaceRequestDoneMsg.ReadOnly` keeps a finished read from freeing the in-flight slot a mutation is holding.

**One mutation in flight.** The busy check covers the agent's request, the workspace progress modal that the create/add-repos/fork/complete flows drive, and the interactive modals that are about to start one of those flows. The keyboard flows are refused while an agent request holds the slot, which makes the two sources mutually exclusive rather than merely ordered.

**Bounded waits, uncancelled work.** The HTTP side waits with a deadline and answers with a machine-readable JSON envelope (`status`, `reason`, `applied`, `message`, `data`). The `applied` field is the one callers branch on:

| Situation | HTTP | reason | applied |
|-----------|------|--------|---------|
| Request never accepted by the TUI | 503 | `not_accepted` | `no` |
| Queued past its deadline, dropped | 503 | `expired` | `no` |
| Accepted, no answer in time | 503 | `timeout` | `unknown` |
| Ran and failed | 4xx/5xx | operation-specific | operation-specific |

A timeout abandons the *wait*, not the work: the TUI still runs the operation to completion, records provenance, writes the events row, and logs it. Nothing is left half-applied because the operation — not the HTTP round trip — is the unit of atomicity. Requests still sitting in the queue at their deadline are dropped instead of started, so an operation can never begin after its caller was told nothing happened.

**Observability.** Every request that resolved a task writes a `workspace_<op>` row to the events table before replying, plus a debug-log line in the TUI on completion.

## Workspace API

Four endpoints ride the serialization path above.

| Method | Path | Op | Answers with |
|---|---|---|---|
| `GET` | `/api/workspace` | `list` | `slots[]` — every working copy the task holds |
| `GET` | `/api/workspace/repos` | `repos` | `repos[]` — what the metarepo makes available |
| `POST` | `/api/workspace/add` | `add` | `slot` — the working copy created |
| `DELETE` | `/api/workspace/slot` | `remove_slot` | `slot` — the working copy removed |

**One way to name a task.** All four go through `Model.resolveRequestTask`: an explicit `task` name wins, and otherwise the caller's `cwd` is matched against live tasks' `workspace_dir` (longest prefix, path-boundary aware, symlinks resolved). Matching on `workspace_dir` rather than `tasks.cwd` matters because the cwd tracks wherever Claude has `cd`'d, while the workspace dir is fixed. The resolved name is echoed back on every response so a cwd-identified caller learns what krang calls it.

**Recorded versus scanned.** The listing is the `workspace_repos` rows joined with a directory scan, and reports which is which. A recorded slot carries a VCS identity and a base revision krang can act on; a scanned one is a directory whose repo and label were guessed from its name. Both are listed — hiding the second would make the API disagree with `ls` — but only the first is authoritative. A recorded row whose directory has been deleted is listed with `exists: false` rather than dropped, because that inconsistency is exactly what a caller needs to see.

**Slots come from the canonical repo.** Every working copy is created from `repos_dir/<repo>`, never from a sibling slot: branching off a neighbour would inherit its in-progress state and tie the new slot's lifetime to the neighbour's VCS identity. The `--base` revset flows through `SlotIdentity.Base` into `jj workspace add -r` or `git worktree add <commitish>`, defaulting to remote-default-branch detection, and the *effective* base is what lands in `base_revision` — recording the bookmark name after the fact would point somewhere else once it moves.

**Refusals rather than guesses.** Three cases the API declines instead of picking an answer for:

| Situation | Reason | Why not just do it |
|---|---|---|
| Repo already in the task, no label given | `label_required` (400) | Auto-numbering is fine when a human sees the result; an agent handed `alpha--2` unasked can't tell its checkouts apart. The refusal suggests a free label. |
| Workspace shared by several tasks | `shared_workspace` (409) | Nothing says which task owns a slot. The row would name one, and completing it would forget an identity another task is still working in. Fork independently. |
| Removal would destroy unsaved work | `unsaved_work` (409) | `blockers[]` names what. `{"force": true}` proceeds. Git-only by nature: forgetting a jj workspace leaves its commits in the source repo's store. |

A per-task cap of `workspace.MaxSlotsPerTask` (4) working copies bounds sprawl on the API path, with the refusal naming what could be removed to make room. The human's repo picker is not capped.

**Removal order.** Forget the recorded VCS identity, remove the directory, drop the row — stopping at the first failure so all three stay in step and the identical request can be retried. Removing a repo's last slot is not special-cased: it is how a repo leaves a task, through the same gates, and the response says `data.repo_dropped`. In `single_repo` the workspace directory *is* the initial checkout and *is* the task's cwd, so removing that slot would be a task teardown; it is refused with `workspace_root` regardless of `force`. In `multi_repo` — what this API is really for — the task's cwd is the workspace container and no slot is ever the cwd root.

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

**workspace_repos table:** id, task_id (FK), repo_name, dir_name, vcs, vcs_name, slot_label, base_revision, created_at

One row per working copy inside a task's workspace directory, written by the creation path for every slot including a task's initial ones. `base_revision` holds the revset or commit-ish the working copy actually started from — the caller's `--base` when it named one, otherwise the remote default branch as detected at creation time. It is resolved before the working copy is made and recorded from the same value the VCS was given, because a bookmark name recorded after the fact points wherever the bookmark has since moved. Cleanup reads these rows to learn which repo under the repos dir a directory came from and which jj workspace / git branch to forget, instead of inferring both from the directory name. Unique on (task_id, dir_name) and (repo_name, vcs_name). Rows are dropped when the task completes, releasing its VCS identities along with its name. Directories with no row — one-off clones made by hand, workspaces predating the table that backfill skipped — still get the old directory-name derivation as a best-effort fallback. The events table can't serve this purpose: it's trimmed on every reconcile.

Recording every slot matters because the reconcile backfill only fires for tasks with *zero* rows: a task recorded partially would never have the rest filled in.

## Import

Import existing Claude sessions by name + session ID. Krang auto-discovers the working directory by searching `~/.claude/projects/` for the session file. The encoded project directory name (Claude replaces non-alphanumeric chars with `-`) is decoded by walking the filesystem to resolve ambiguous hyphens in path names.

## CWD Tracking

Task cwd updates live from hook event `cwd` field, which reflects Claude's current working directory (not just the launch directory). Displayed as relative paths when under krang's working directory.

## Development

- `KRANG_DB=.krang-dev.db` and `KRANG_CONFIG=.krang-dev-config.yaml` isolate dev state (set in mise.toml)
- Uses `jj` for version control, never `git` commands
- Temp files use `NOCOMMIT-` prefix to avoid jj snapshotting them into commits
- Claude sandbox wrapper is configurable via `krang setup`
