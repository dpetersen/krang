# Krang

TUI task orchestrator for managing multiple Claude Code sessions via tmux.

## Architecture

- **Go + Bubble Tea** TUI running in a tmux window
- **SQLite** per-instance at `~/.local/share/krang/instances/<encoded-cwd>/krang.db` (override with `KRANG_DB` env var)
- **Claude Code command hooks** via relay script that reads `KRANG_STATEFILE` for the dynamic port
- **AI summaries** via `claude -p --model haiku` with structured JSON output (includes current summary in prompt to reduce churn)
- **Attention classification** via Haiku on Stop events to distinguish "done" vs "waiting"
- Claude spawned via named sandbox profiles (configurable per-task)

## Multi-Instance Support

Multiple krang instances can run simultaneously for different working directories. Each instance gets:
- Its own dynamic port (bound to `:0`) with state file at `~/.local/state/krang/instances/<encoded-cwd>/krang-state.json`
- Its own SQLite database at `~/.local/share/krang/instances/<encoded-cwd>/krang.db`
- Its own tmux sessions: `k-<instanceID>` (active) and `k-<instanceID>-parked`
- Instance ID format: `<basename>-<4 hex SHA-256 of full path>` (e.g., `krang-496d`)

## File Locations

| Path | Purpose | XDG category |
|------|---------|-------------|
| `~/.config/krang/config.yaml` | Named sandbox profiles, window colors, attention classification, default VCS, GitHub orgs | Config |
| `~/.config/krang/hooks/relay.sh` | Static relay script (Claude settings.json points here) | Config |
| `~/.local/share/krang/instances/…/krang.db` | Per-instance SQLite database | Data |
| `~/.local/state/krang/instances/…/krang-state.json` | Per-instance port file (ephemeral, exists while running) | State |

## Task States

- **Active** — tmux window in krang's session, Claude running
- **Parked** — tmux window moved to parked session, still running
- **Frozen** (DB: `dormant`) — no tmux window, session ID saved for `--resume`
- **Completed/Failed** — terminal states; names freed for reuse

## Keybinding Model

The TUI uses a two-tier keybinding system: a minimal set of global keys on the main screen, and per-task actions in a detail modal.

Hints are placed in three zones below the table:

- **Table toolbar** — list-specific: `/` filter, `s` sort, `T` sparkline window, `j/k` nav, plus task count
- **Action bar** — task actions: `n` new, `enter` focus, `tab` detail, `c` complete (shown when a task is selected)
- **Footer** — global: `:` command palette, `?` help, `q` quit

**Command palette** (`:`): modal overlay listing rare commands (sit rep, import, compact windows). Navigate with `j/k`, run with `enter`, close with `esc`.

**Detail modal** (`Tab` on a selected task): centered overlay showing task info (cwd, age, flags, fork lineage, shared workspace info, background processes) and context-sensitive actions. Toggle keys: `f` freeze/unfreeze, `p` park/unpark. Also: `c` complete, `d` fork, `+` companion, `F` flags, `W` add repos, `Enter` focus. Closes with `Esc`/`Tab`.

**Complete** (`c`): unified action replacing the former separate kill/complete. Shows consequence-aware confirmation modal stating what will happen (process stop, workspace deletion). For shared workspaces, the confirmation shows which other tasks share the workspace and that it will NOT be deleted. Sets `StateCompleted` + `AttentionDone`. `StateFailed` is only set by the reconciler when windows vanish unexpectedly.

**Fork** (`d` in detail modal): creates a new task that forks from the selected task's Claude conversation. Two workspace modes:
- **Independent** (default): new workspace via `jj duplicate` + `jj workspace add` (jj) or `git worktree add` + file copy (git). Fully separate — sibling commits, no rebase interaction.
- **Shared**: same workspace, just forks the conversation. Warning shown about concurrent edit risk. Workspace cleanup deferred until last task using it completes.
Session files are copied to the new workspace's Claude project directory so `--resume --fork-session` can find them. Forked tasks track lineage via `source_task_id` (shown as "forked from" in detail modal).

## Modal Overlays

Modals (detail, confirm, help, command palette, workspace wizard) render as centered boxes over a dimmed background using `overlayCenter()` in view.go. The background is the full normal view (header, table, hint bars, debug log) with ANSI faint applied. The `renderNormalView()` helper provides the background for modes that need it.

The workspace wizard (task creation form, repo picker, and workspace creation progress) uses wider modals (2/3 terminal width) via `wideModalWidth()`. The workspace progress modal shows a per-repo checklist with status icons (spinner for active, ✓/✗ for done/failed), a scrollable log of clone output, and supports esc-to-cancel. Progress is incremental — each repo clone is a separate `tea.Cmd`, so the UI updates between clones.

## Table # Column

The `#` column shows the actual tmux window index for active tasks (so users can `Ctrl-B <n>` to jump). Parked and frozen tasks show blank. Indexes are fetched from tmux alongside task refreshes via `tmux.WindowIndexes()`.

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
- `internal/classify/` — Haiku-based attention classification (done vs waiting) on Stop events
- `internal/summary/` — ANSI stripping, `claude -p` wrapper, summary pipeline
- `internal/proctree/` — process tree walking, noise/age filtering, leaf-only display for background child process awareness
- `internal/workspace/` — `krang.yaml` parsing, workspace creation/destruction, VCS operations (jj workspace add, git worktree add)
- `internal/github/` — GitHub repo discovery via `gh` CLI (search, clone)
- `internal/tui/` — Bubble Tea model, view, keybindings, messages, theming

## Theming

Styles are derived from a `Theme` struct with semantic color roles (Title, Error, Active, etc.). The `Styles` struct holds precomputed lipgloss styles built via `BuildStyles(theme)` and retains a `theme` field for direct color access. Available themes: `classic` (original ANSI 256 colors), `catppuccin-mocha` (default), `catppuccin-latte`, `catppuccin-frappe`, `catppuccin-macchiato`. Set via `"theme"` field in config.yaml.

Color is used throughout: accent-colored key hints in the footer, state-colored counts in the header (parked blue, frozen gray, active default white), accent-colored "Events" label in the debug log, and differentiated timestamps (faint accent) vs message text (muted) in log entries.

## Attention Classification

On `Stop` hook events, krang classifies Claude's `last_assistant_message` via Haiku to determine whether Claude is asking a question (`AttentionWaiting`) or finished work (`AttentionDone`). This runs as an async `tea.Cmd` — the task shows a spinner in the Attn column while classification is in flight, with no color change until the result arrives.

A `classifyGen map[string]uint64` generation counter on the Model handles cancellation: every hook event bumps the counter, and stale classification results are discarded. On error, falls back to `AttentionWaiting`.

When classification is active, `handleHookEvent` skips setting `AttentionWaiting` on Stop — the classifier sets the final state. When disabled (`"classify_attention": false` in config), Stop immediately sets `AttentionWaiting` as before.

**Attention color scheme:**

| State | Color | Label | Meaning |
|-------|-------|-------|---------|
| ok | uncolored | "ok" | Claude is working |
| done | green | "done" | Claude finished work |
| wait | yellow | "wait" | Claude is asking a question |
| PERM | bold red | "PERM" | Permission prompt blocking |
| ERR | red | "ERR" | Stop failure |

The spinner has no hardcoded color — it inherits the row style from `StyleFunc`.

## Activity Sparklines

The task table includes an "Activity" column showing a 20-character sparkline of recent hook events. Each character is a Unicode block (`▁▂▃▄▅▆▇█`) where height represents event density and color represents what Claude was doing.

**Stacked colors**: Each cell uses foreground + background colors to show two event types simultaneously. The higher-priority type gets the foreground (the block character), the secondary type gets the background color behind it.

**Color mapping**: Accent = tool calls, Active = working, Warning = waiting, Done = done, Danger = permission, Error = error, Dormant = idle.

**Sticky state**: State-transition events (PermissionRequest, Stop, etc.) fill forward into subsequent empty buckets until the next event clears them. This means a permission block shows continuous red, not a single blip.

**Time windows**: `T` cycles all tasks through 1m / 10m / 60m. At 20 chars: 3s, 30s, or 3min per bucket. Data comes from the existing `events` table, queried every 5 seconds. Events older than 2 hours are trimmed on the reconcile tick.

The sparkline column gets special treatment in the table's `StyleFunc` — no foreground color is set, preserving the per-character ANSI colors embedded in the rendered string.

## Async Feedback

Lifecycle operations (park, unpark, freeze, unfreeze, complete) show an animated spinner with operation label (e.g. "freezing...") in the Attn column via `bubbles/spinner`. A `pendingOps` map tracks in-flight operations, cleared by `pendingOpDoneMsg` when the action completes.

## Help System

Help (`?`) renders as a centered modal overlay with glamour-rendered markdown content, scrollable with j/k. Content is defined in `buildHelpMarkdown()` in view.go.

## Task Creation

Task creation and import use `charmbracelet/huh` forms rendered as modal overlays. The task table uses `lipgloss/table`. Task names must match `[a-zA-Z0-9_-]+`.

## Workspaces

Optional per-task isolated directories configured via `krang.yaml` at the metarepo root. See `docs/workspaces.md` for full details.

- **`workspace_strategy: single_repo`** — pick one repo, workspace dir is a worktree/workspace
- **`workspace_strategy: multi_repo`** — pick multiple repos (with optional set grouping via a custom toggle-list component), workspace dir contains worktrees/workspaces
- **No strategy** — CWD picker (original behavior)
- Git repos use `git worktree add` with `krang/<task-name>` branches; jj repos use `jj workspace add`
- `.worktreeinclude` files in source repos specify gitignored files to copy into new worktrees
- Workspaces destroyed on task complete (git worktree remove + branch -d / jj workspace forget + rm -rf)
- Unpushed branches are kept on cleanup; completion modal warns about them
- `W` in the detail modal adds repos to existing multi_repo workspaces
- Sandbox profiles of type `command` support Go templates (`{{.KrangDir}}`, `{{.TaskCwd}}`, `{{.TaskName}}`, `{{.ReposDir}}`) for granting sandboxed tasks access to metarepo config files
- **GitHub repo discovery** — the repo picker has a tabbed interface (`Tab` toggles Local / Remote). The Remote tab searches GitHub orgs via `gh` CLI and clones repos into the repos dir. Config orgs show as a selectable list; "Other..." allows manual entry. Search is debounced (300ms). After cloning, the Local tab refreshes to show the new repo.
- **`default_vcs`** — configurable in `config.yaml` (user-level) or `krang.yaml` (project-level, takes precedence). Controls whether remote clones use `git clone` or `jj git clone`. Defaults to `git`.
- **`github_orgs`** — configurable in both `config.yaml` and `krang.yaml`, merged with dedup. Saved orgs appear in the org select list on the Remote tab.

## Building and Running

```
mise run run              # build, install hooks, launch TUI (uses dev DB)
mise run test             # run unit tests
mise run test:integration # run integration tests (requires tmux)
mise run build            # build binary only
mise run setup            # install Claude Code hooks only
```

Must be run inside tmux. Uses `jj` for version control, not `git`.

Development uses `KRANG_DB=.krang-dev.db` and `KRANG_CONFIG=.krang-dev-config.yaml` (set in mise.toml) to isolate from production paths.

## Testing

Unit tests (`mise run test`) cover business logic, config, DB, workspace operations (including git worktree and jj workspace edge cases), and command building. They run fast and don't require tmux.

Integration tests (`mise run test:integration`) exercise the full TUI lifecycle in real tmux with a fake Claude binary. They test task creation, hook event routing, park/unpark, freeze/unfreeze, complete, reconciliation, and forking. These must be run inside tmux and take ~30 seconds.

**Run both unit and integration tests before considering a feature complete.** The integration tests catch bugs that unit tests can't (e.g., tmux version-specific behavior, key sequence regressions, session adoption races).

The fake Claude binary (`internal/testutil/fakeclaude/`) accepts the same CLI flags as real Claude, writes a manifest file for test inspection, creates minimal session files, and blocks until SIGINT. The `KRANG_CLAUDE_CMD` env var overrides the Claude binary path for testing.

## Graceful Shutdown

Tasks are shut down via SIGINT to the Claude process (found via `pgrep -P <shell_pid>`), with a 5-second timeout before falling back to `tmux kill-window`. DB state is updated before killing windows to prevent reconcile races.

On krang exit, parked tasks are offered for freezing. If frozen (or none exist), the parked session is cleaned up automatically.

## Hook Events

Krang listens for: `SessionStart`, `UserPromptSubmit`, `Stop`, `PermissionRequest`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `SubagentStart`, `SubagentStop`, `TaskCompleted`, `StopFailure`, `Notification`, `SessionEnd`. Events matched to tasks by `session_id`. Resumed sessions adopted via cwd matching on `SessionStart`.

Events may include `agent_id` and `agent_type` fields identifying which subagent fired them. Krang tracks active subagents per task via `SubagentStart`/`SubagentStop` events and displays a 🤖N indicator in the Attn column. Subagent state is cleared on `Stop` or `SessionEnd` (main agent finished).

Hooks are `type: "command"` entries in `~/.claude/settings.json` pointing to the relay script. The relay script only forwards events when `KRANG_STATEFILE` is set (which krang does for sessions it launches), so standalone Claude sessions are unaffected.

## Import

Import discovers the cwd automatically by searching `~/.claude/projects/` for the session ID file. The encoded project directory name is decoded by walking the filesystem to handle ambiguous hyphens in path names.

## CWD Tracking

Task cwd updates live from hook event payloads. Displayed as relative paths when under krang's working directory, tilde-ified otherwise.

## Sort Modes

- **Created** (default) — all tasks, stable creation order
- **Priority** — active tasks only, sorted by attention: permission > error > waiting > ok > done

## Sandboxing

Krang supports named sandbox profiles configured in `config.yaml`. Each profile has a `type` (currently only `command`) and type-specific fields. Tasks can be assigned a specific profile at creation time or via the flag edit form (`F` in detail modal); changing the profile on an active task triggers a relaunch.

```yaml
sandboxes:
  default:
    type: command
    command: "safehouse --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG"
  cloud-tools:
    type: command
    command: "safehouse --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG --env-pass AWS_PROFILE"
default_sandbox: default
```

Selecting "(none)" in the sandbox picker or not configuring any profiles runs Claude unsandboxed (shown with ☠ in the task table, like `DangerouslySkipPermissions`).

Krang itself runs unsandboxed; only the Claude processes inside task windows are sandboxed. The sandbox must pass through `KRANG_STATEFILE` (required) and `KRANG_DEBUG` (optional) env vars, and allow read access to `~/.local/state/krang/` (state file) and read+execute on `~/.config/krang/hooks/` (relay script). No write access to krang paths is needed from inside the sandbox. See README.md for full details.

## Debugging

The Debug task flag (`KRANG_DEBUG=1`) enables relay script logging to `/tmp/krang-debug.log`. Logs the full JSON payload of each hook event and the HTTP status of delivery to krang. Requires relaunch to take effect.
