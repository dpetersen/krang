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

Frozen tasks can outlive their transcript: Claude Code deletes session files older than `cleanupPeriodDays` (30 by default), leaving a session ID that resolves to nothing. `task.SessionResumable()` checks for this, `refreshTasks` evaluates it for dormant tasks on every refresh, and the TUI marks affected rows with ⚠. `Manager.Wake` refuses up front rather than launching a window that dies with "no such session".

## Keybinding Model

The TUI uses a two-tier keybinding system: a minimal set of global keys on the main screen, and per-task actions in a detail modal.

Hints are placed in three zones below the table:

- **Table toolbar** — list-specific: `/` filter, `s` sort, `T` sparkline window, `j/k` nav, plus task count
- **Action bar** — task actions: `j/k` nav, `n` new, plus `enter` focus and `tab` detail when a task is selected. Everything destructive (`c` complete, `d` fork, `W` add repos) lives one level in, in the detail modal.
- **Footer** — global: `:` command palette, `?` help, `q` quit

**Command palette** (`:`): modal overlay listing rare commands (sit rep, import, compact windows). Navigate with `j/k`, run with `enter`, close with `esc`.

**Detail modal** (`Tab` on a selected task): centered overlay showing task info (cwd, age, flags, fork lineage, shared workspace info, background processes) and context-sensitive actions. Toggle keys: `f` freeze/unfreeze, `p` park/unpark. Also: `c` complete, `d` fork, `+` companion, `F` flags, `W` add repos, `Enter` focus. Closes with `Esc`/`Tab`.

**Complete** (`c`, from the detail modal): unified action replacing the former separate kill/complete — it is the *only* path to a terminal state a human drives, so it is the only place identities are released. Shows a consequence-aware confirmation modal stating what will happen: process stop, and the workspace path with a count of every working copy it holds (slots included, since "the workspace" stopped being an answer once a task could hold three checkouts of one repo). Uncommitted and unpushed work is named per *slot directory*, and each unpushed warning reports that slot's own surviving branch (`workspace.GitBranchFor`) rather than `krang/<task>`, which is only the initial checkout's. Only git working copies are checked, the same line the removal API's blockers draw. For shared workspaces, the confirmation shows which other tasks share the workspace and that it will NOT be deleted. Sets `StateCompleted` + `AttentionDone`. `StateFailed` is only set by the reconciler when windows vanish unexpectedly — a diagnosis, not a teardown, so it deliberately leaves provenance rows alone and a later `Complete` on that failed task still releases them.

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
- `internal/hooks/` — HTTP server for Claude Code hook events and workspace requests, relay script + settings.json installer
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

Optional per-task isolated directories configured via `krang.yaml` at the metarepo root. See `docs/design/workspaces.md` for full details.

- **`workspace_strategy: single_repo`** — pick one repo, workspace dir is a worktree/workspace
- **`workspace_strategy: multi_repo`** — pick multiple repos (with optional set grouping via a custom toggle-list component), workspace dir contains worktrees/workspaces
- **No strategy** — CWD picker (original behavior)
- Git repos use `git worktree add` with `krang/<task-name>` branches; jj repos use `jj workspace add`
- `.worktreeinclude` files in source repos specify gitignored files to copy into new worktrees
- Workspaces destroyed on task complete (git worktree remove + branch -d / jj workspace forget + rm -rf)
- **Slots** — a task may hold more than one working copy of the same repo. Every working copy is created through `workspace.CloneRepoAs` under a `SlotIdentity{TaskName, RepoName, Label}`, which derives all its names: `DirName()`, `VCSName()` (jj workspace name), and `GitBranch()` (`krang/<VCSName>`). An **empty label means the task's initial working copy** and keeps the pre-slot names (dir `<repo>`, VCS identity `<task>`) so nothing existing is renamed. A labeled slot gets dir `<repo>--<label>`, jj workspace `<task>--<repo>--<label>`, branch `krang/<task>--<repo>--<label>`. Labels are lowercase alnum with single dashes (`ValidateSlotLabel`); `--` is reserved as the separator so a name can always be split back apart. `ResolveSlotIdentity` auto-numbers (2, 3, …) to the first free discriminator when no label is given.
- **Refusal, not overwrite** — before creating anything, the computed jj workspace name is checked against `jj workspace list` in the source repo and the git branch against `git branch --list`, and a computed slot dir that spells a managed repo's name is rejected. The one reclaim krang still does is a leftover `krang/<task>` branch on task-name reuse, and only when `git branch -d` agrees nothing is lost — cleanup deliberately keeps unpushed branches, so creation must not force-delete them.
- **Repo provenance** — the `workspace_repos` table records where each working copy in a workspace came from (repo name, dir name, vcs, vcs identity name, slot label, base revision). Creation writes a row for *every* working copy, including initial ones — the reconcile backfill only fires for tasks with zero rows, so a partial record would never be completed. `Manager.CreateSlot` creates and records in one step; the TUI's task-creation flow builds the workspace before the task row exists, so provenance rides on the progress entries and is written via `Manager.RecordSlot` once the task is created. Cleanup forgets the *recorded* jj workspace name / git branch rather than inferring it from directory names. Workspaces predating the table are backfilled on reconcile (`Manager.backfillWorkspaceRepos`); directories with no row fall back to the directory-name derivation. `Manager.Complete` releases the rows (`releaseWorkspaceRepos`) so a reused task name doesn't collide with the `(repo_name, vcs_name)` unique constraint — normally by deleting them, but by *reassigning* them to the oldest surviving task when the workspace directory is shared. There the working copies outlive the task, so the survivor is the one that will eventually tear the directory down and it needs every identity to forget; deleting would strand them, and a derivation can never name a slot.
- **Cleanup is per working copy, not per repo** — `DestroyRepoList` returns one entry per working copy, recorded rows first in row order, so a task holding three checkouts of one repo gets three forgets under three identities. A recorded row whose directory is already gone stays in the list: the identity it names still has to be forgotten. The filesystem scan behind the rows only applies where the workspace directory is a *container* — in `single_repo` the workspace directory IS the checkout, so scanning its subdirectories would invent slots out of vendored checkouts. `single_repo` is otherwise outside the slot system: its cleanup still goes through `ForgetSingleRepoWorkspace`, which asks every repo in turn.
- **Reading a workspace back** — `PresentDirs` is the raw scan (directory names). `PresentSlots` resolves each directory to the repo it holds, preferring recorded rows and falling back to `ParseSlotDirName`. `PresentRepos` returns the distinct repos, which is what the picker hides — a second slot of a repo must not read as an unknown repo of its own. Forking is still directory-oriented and not slot-aware. What keeps that from being dangerous is the shared-workspace suppression: completing a fork that shares its owner's workspace destroys nothing and forgets nothing, so the owner's slots — which the fork never had a claim on — survive it (`TestCompletingAForkLeavesTheSharedWorkspaceAlone`). An *independent* fork of a workspace holding labeled slots is a gap, not a feature: `ForkRepo` treats each directory name as a repo name, so `alpha--tests` resolves to a repo that doesn't exist and the fork fails at creation time rather than producing something cleanup would mishandle.
- Unpushed branches are kept on cleanup (`git branch -d`, never `-D`); the completion modal warns about them and names the branch each slot leaves behind
- `W` in the detail modal adds repos to existing multi_repo workspaces
- Sandbox profiles of type `command` support Go templates (`{{.KrangDir}}`, `{{.TaskCwd}}`, `{{.TaskName}}`, `{{.ReposDir}}`) for granting sandboxed tasks access to metarepo config files
- **GitHub repo discovery** — the repo picker has a tabbed interface (`Tab` toggles Local / Remote). The Remote tab searches GitHub orgs via `gh` CLI and clones repos into the repos dir. Config orgs show as a selectable list; "Other..." allows manual entry. Search is debounced (300ms). After cloning, the Local tab refreshes to show the new repo.
- **`default_vcs`** — configurable in `config.yaml` (user-level) or `krang.yaml` (project-level, takes precedence). Controls whether remote clones use `git clone` or `jj git clone`. Defaults to `git`.
- **`github_orgs`** — configurable in both `config.yaml` and `krang.yaml`, merged with dedup. Saved orgs appear in the org select list on the Remote tab.

## Workspace Requests

Workspace mutations asked for from outside the TUI (a Claude session, a
CLI subcommand) go through the hook HTTP server and are serialized by
the Bubble Tea process, which is the single writer. See
`internal/hooks/workspace.go` and `internal/tui/workspacereq.go`.

### Endpoints

| Method | Path | Op | Queued? |
|---|---|---|---|
| `GET` | `/api/workspace` | `list` | no (read-only) |
| `GET` | `/api/workspace/repos` | `repos` | no (read-only) |
| `POST` | `/api/workspace/add` | `add` | yes |
| `DELETE` | `/api/workspace/slot` | `remove_slot` | yes |

Every endpoint identifies the task the same way, through one shared
helper (`Model.resolveRequestTask`): an explicit `task` name wins, and
otherwise `cwd` is matched against live tasks' `workspace_dir` with a
path-boundary-aware longest-prefix match. The match is on
`workspace_dir`, not `tasks.cwd`, because the cwd follows Claude around
as it `cd`s while the workspace dir is fixed for the task's life. The
resolved name is echoed back as `task` on every response.

- **`GET /api/workspace`** — every working copy the task's workspace holds, as `slots[]` with exactly `{dir, repo, canonical_repo_path, vcs, vcs_name, slot, base, exists, recorded}` (no `omitempty`; a caller branching on `exists` must not have to tell `false` from absent). Recorded `workspace_repos` rows come first in row order; repo-looking directories with no row follow with `recorded: false`. A recorded row whose directory is gone is still listed, with `exists: false`.
- **`GET /api/workspace/repos`** — every directory in `repos_dir` as `repos[]` of `{name, in_task, sets}`. `in_task` comes from `PresentRepos` (recorded rows preferred, directory scan as fallback), so a second slot of a repo doesn't read as a second repo. Repos named only in a `krang.yaml` set but not cloned are not listed — you can't add what isn't there.
- **`POST /api/workspace/add`** — one verb for both "a repo this task doesn't have" and "another checkout of one it does"; which it turns out to be follows from the workspace, not the URL. A repo the task already holds requires an explicit `label`, and the refusal suggests a free one (auto-numbering is fine when a human can see the result, but an agent handed `alpha--2` unasked has no idea which checkout is which). `base` plumbs a revset/commit-ish through `SlotIdentity.Base` to `jj workspace add -r` / `git worktree add <commitish>`; empty means detect the remote default branch, and either way the effective value is recorded in `base_revision`. Slots are always created from the canonical repo under `repos_dir`.
- **`DELETE /api/workspace/slot`** — names the slot by `dir` (what the listing reports) or by `repo` plus optional `label`. Forgets the *recorded* VCS identity, removes the directory, drops the row, in that order, stopping at the first failure so the three stay in step and the identical request can be retried.

### Policies the API enforces

- **Slot cap** — `workspace.MaxSlotsPerTask` (4) working copies per task. The refusal (`slot_limit`) names the slots that could be removed. The cap is on the API only; the human's repo picker is trusted.
- **Shared workspaces refuse adds** — when two tasks share a `workspace_dir`, nothing says who owns a slot: the row would name one task and completing it would forget an identity the other is still working in. `shared_workspace` (409) keeps that ambiguity visible rather than encoding an arbitrary answer. Fork independently instead.
- **Unsaved-work gate on removal** — `HasUncommittedChanges` / `HasUnpushedCommits` block the removal with `unsaved_work` (409) and a machine-readable `blockers[]`; `{"force": true}` proceeds. The gate is git-only by nature: forgetting a jj workspace leaves its commits — including the working-copy commit — in the source repo's store where `jj log` still finds them, so there is nothing to lose.
- **Last slot of a repo** — not special-cased. Removing it is how a repo leaves a task, through the same gates. The response reports `data.repo_dropped`.
- **Workspace root** — in `multi_repo` the task's cwd is the workspace *container* and no slot is ever the cwd root. In `single_repo` the workspace dir IS the initial checkout and IS the cwd, so removing that slot is a task teardown wearing a slot removal's clothes: refused with `workspace_root`, force or not. A cwd that has drifted *into* a slot is not checked — the agent asking is the one standing there.
- **Missing directory** — a recorded slot with nothing on disk is refused with `slot_missing` (409) until forced, so the inconsistency surfaces before it's papered over.

### Mechanism

- **Request type** — `hooks.WorkspaceRequest{Op, TaskName, Cwd, Repo, Label, Dir, Base, Force, Deadline, Reply}`. New operations add typed fields rather than a generic map, so handlers and the TUI executor can't drift. Same for the response payloads (`Slots`, `Repos`, `Slot`, `Blockers`). Callers name the *task*, not its ID.
- **Delivery** — the server puts the request on a channel; `Model.waitForWorkspaceRequest` receives it inside a `tea.Cmd` and re-arms, exactly like `waitForHookEvent`.
- **Reads skip the queue.** `WorkspaceOp.ReadOnly()` is true for `list` and `repos`; those start immediately in `handleWorkspaceRequest` instead of queuing. The queue exists to keep two writers off one workspace, and a listing is not a writer. Queuing it would mean waiting on `workspaceBusy()`, which stays true for as long as the human leaves a modal open. The cost is that a listing can catch a workspace mid-clone — which reads out as `recorded: false`, i.e. honestly. `workspaceRequestDoneMsg.ReadOnly` keeps a finished read from freeing the in-flight slot a mutation is holding.
- **The Update loop never blocks.** It appends the request to a FIFO queue. `startNextWorkspaceRequest` runs after every message (via the `Update` wrapper around `update`) and launches the head of the queue as a `tea.Cmd` when nothing else is mutating a workspace. Completion arrives as `workspaceRequestDoneMsg`, which frees the slot and lets the next one start.
- **One at a time, across both sources.** `workspaceBusy()` is true for an in-flight request, an unfinished `wsProgress`, or an open workspace modal (wizard, repo picker, fork dialog, complete confirmation). The keyboard flows share the guard in the other direction: `n`, `e`, `d`, and `c`-on-a-workspace-task are refused (with a debug-log line) while an agent request is in flight.
- **Timeouts don't cancel work.** The HTTP helper `submitWorkspaceRequest` waits a bounded time (`Server.WorkspaceTimeout`, default 60s). Before the TUI accepts the request, a timeout means it definitely never ran (`not_accepted`/`expired`, `applied: "no"`). After acceptance, giving up returns 503 `{"reason":"timeout","applied":"unknown"}` — the operation runs to completion anyway and still records its provenance, events row, and log line. A queued request past its deadline is dropped rather than started, so work never begins after the caller was told nothing happened.
- **Observability** — every request that resolved a task writes a `workspace_<op>` events row (before the reply, so abandoned callers still leave a trail) and debug-log lines on start and completion. The row records the *resolved* task name, since a cwd-identified caller sends none. While a mutation is in flight the TUI shows a status line under the task table naming the op, the task, and the target slot, plus the depth of the queue behind it. It is a status line rather than the progress modal the keyboard flows use: an API request arrives at a moment nobody chose, and a full-screen modal would swallow whatever the human was mid-way through. **Esc does not cancel an API-initiated mutation**, and the line says so — matching the W-key path, where esc stops the checklist before the *next* repo and has never interrupted a clone already underway. A request still *queued* has not started, and its own deadline drops it. The detail modal lists every working copy the workspace holds, grouped by repo, with slot labels and bases.
- **Failure reasons** — `invalid_request`, `no_workspace`, `unknown_repo`, `label_required`, `slot_limit`, `ambiguous_slot`, `unsupported_operation` → 400; `unknown_task`, `unknown_slot` → 404; `unsaved_work`, `shared_workspace`, `slot_missing`, `workspace_root` → 409 (conflicts with the state of the world, so the identical request works once resolved — for `workspace_root`, once the task is completed); `unavailable`, `not_accepted`, `expired`, `timeout` → 503; `operation_failed` → 500. `TestWorkspaceStatusCodesCoverEveryReason` pins the table, because a deliberate refusal that falls through to the 500 default reads as krang breaking rather than krang declining.

### The `krang workspace` CLI

`cmd/workspace.go` is the agent-facing front end for those endpoints — a sibling of `setup`/`teardown`, so it runs outside tmux and outside the TUI. The bindings are thin: everything from finding the instance to choosing the exit code lives in `internal/workspaceclient`, which is tested against an `httptest` server and against a real `hooks.Server` end to end (`internal/workspaceclient/endtoend_test.go`).

| Subcommand | Endpoint | Flags beyond `--task`/`--cwd`/`--json` |
|---|---|---|
| `krang workspace list` | `GET /api/workspace` | none |
| `krang workspace repos` | `GET /api/workspace/repos` | none |
| `krang workspace add` | `POST /api/workspace/add` | `--repo` (required), `--label`, `--base` |
| `krang workspace remove` | `DELETE /api/workspace/slot` | `--dir` or `--repo`/`--label`, `--force` |

- **Transport** — `KRANG_STATEFILE` names the state file, the state file names the port, and the client posts to `127.0.0.1:<port>`. There is no liveness probe: the first request is the probe. The client's timeout is `hooks.DefaultWorkspaceTimeout + 10s` on purpose — the server has to be the one that times out, because its timeout produces an envelope saying whether the work may still land, and a client that gave up first would replace that with a dead socket.
- **The caller has no eyes.** `--cwd` defaults to the process's cwd, so `krang workspace list` works with no arguments from inside a workspace; `--task` overrides and suppresses the cwd rather than sending both. `add` prints the new absolute path and nothing else. `--json` prints the response bytes verbatim (not a re-encode), so fields krang grows later survive the trip.
- **Exit codes** — `0` ok; `1` unfixable error (bad flags, no state file, unknown task/repo/slot, `operation_failed`, an instance too old to serve the endpoint); `2` a refusal the *identical* command fixes once the caller deals with it (`unsaved_work`, `label_required`, `slot_limit`, `shared_workspace`, `slot_missing`, `ambiguous_slot`); `3` `applied: "unknown"` — may have taken effect, and stderr says outright not to retry blindly; `4` krang never took it (`unavailable`, `not_accepted`, `expired`, or a dead port), so retrying is safe. `applied` is checked before `reason`: "might have happened" outranks every other classification. `workspaceclient.ExitCodeHelp` is the table, and every subcommand's `--help` embeds it along with its endpoint and defaults, because an agent reads one `--help`, not the tree.
- **A 404 is not an unknown.** An instance launched from a build predating the workspace API answers from its router, before any handler. That is a hard guarantee that nothing was applied, so it gets its own error ("relaunch krang") and exit 1 rather than the scary exit 3 that a genuinely ambiguous answer earns.

## Changelog

When fixing a bug or adding a feature, add an entry to the `[Unreleased]` section of `CHANGELOG.md` under the appropriate category (`Added`, `Changed`, `Fixed`, etc.). See `README.md > Cutting a Release` for the full release process.

## Debugging Live Instances

**Never interact with a live krang instance's tmux sessions when debugging.** Krang manages sessions by name (`k-<instanceID>`). If the session gets renamed or a second krang process starts in the same session, window management breaks silently — tasks can't be focused, new windows don't appear, and the instance becomes unrecoverable without manual tmux surgery.

Safe debugging approaches: query the SQLite DB directly, read source code, read log files. Do NOT run tmux commands targeting krang-managed sessions/windows, and never start krang (including via `mise run run`) inside a window managed by another krang instance.

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

Integration tests (`mise run test:integration`) exercise the full TUI lifecycle in real tmux with a fake Claude binary. They test task creation, hook event routing, park/unpark, freeze/unfreeze, complete, reconciliation, and forking. These must be run inside tmux and take ~80 seconds.

**Each test gets its own tmux server**, created with `tmux -L krang-test-<pid>-<nanos>` and destroyed via `kill-server` on cleanup. The harness passes the socket name to the krang binary as `KRANG_TMUX_SOCKET`, and `internal/tmux` routes every invocation through `command()`, which adds `-L` when that variable is set. This is what makes it safe to run the suite from inside a live krang instance: the tests set `HOME` and `KRANG_CLAUDE_CMD` in the server's *global* environment, which on a shared server would be inherited by every window opened afterwards.

Two guard tests enforce the invariant — `internal/tmux.TestNoDirectTmuxInvocations` and `internal/integration.TestNoDirectTmuxInvocations` — by scanning package sources for bare `exec.Command("tmux", ...)` calls. Add new tmux calls via `command()`, `env.tmux(...)`, or `tmuxOn(socket, ...)`; never `exec.Command` directly.

**Run both unit and integration tests before considering a feature complete.** The integration tests catch bugs that unit tests can't (e.g., tmux version-specific behavior, key sequence regressions, session adoption races).

The fake Claude binary (`internal/testutil/fakeclaude/`) accepts the same CLI flags as real Claude, writes a manifest file for test inspection, creates minimal session files, and blocks until SIGINT. The `KRANG_CLAUDE_CMD` env var overrides the Claude binary path for testing.

## Graceful Shutdown

Tasks are shut down via SIGINT to the Claude process (found via `pgrep -P <shell_pid>`), with a 15-second timeout before falling back to `tmux kill-window`. The window is killed before DB state is updated — if the kill fails, the task stays in its current state and the error is surfaced in both the TUI debug log and the DB events table. For workspace tasks, workspace destruction is skipped on kill failure.

On krang exit, parked tasks are offered for freezing. If frozen (or none exist), the parked session is cleaned up automatically.

## Hook Events

Krang listens for: `SessionStart`, `UserPromptSubmit`, `Stop`, `PermissionRequest`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `SubagentStart`, `SubagentStop`, `TaskCompleted`, `StopFailure`, `Notification`, `SessionEnd`. Events matched to tasks by `session_id`. Resumed sessions adopted via cwd matching on `SessionStart`.

### Self-Resuming Tasks

`ScheduleWakeup` and `Monitor` return to the prompt while arranging for Claude to continue with no user input, so `Stop` fires and the task looks idle when it isn't. Krang watches `PostToolUse` for those tool names and marks the task with ⏰ (`internal/tui/selfresume.go`).

There is no completion event to key off — verified empirically: a Monitor that ran to completion produced `PreToolUse`/`PostToolUse` on arming and **nothing at all** when it ended. `TaskCompleted` is registered but does not fire for monitors. The marker is therefore bounded by a deadline read from the tool's own arguments (`delaySeconds`, or `timeout_ms`/`persistent`), which arrive intact in `tool_input`. `ScheduleWakeup{stop: true}` and `UserPromptSubmit`/`SessionEnd` clear it early.

This depends on tool *names*, so it silently stops working if Claude Code renames or replaces these tools. `TestToolInputDecodesRealMonitorPayload` pins the payload shape against a captured real event.

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
