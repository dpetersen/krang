# Future Plans

## Sit Rep Enhancements

- **Parked sit rep variant** — a separate command to get briefings on parked tasks ("what should I do with these?")
- **Store `last_assistant_message`** from Stop hook payloads — gives cheaper context without reading full transcripts
- **Sit rep as modal overlay** — render on top of the existing TUI instead of replacing it (requires terminal compositing or a Bubble Tea overlay approach)

## Task Management

- **Task history view** — see completed/failed tasks with their final summaries, with the ability to revive them

## Hotkey Rework & Task Detail Modal

The current hotkey system shows all keybindings at once in the footer. As the number of actions grows this doesn't scale well, and many keys are only relevant when a specific task is selected.

### Design

- **Global hotkeys only in the footer** — only show always-applicable keys like `n` (new), `I` (import), `?` (help), `q` (quit).
- **Enter** keeps current behavior: focus the selected task's tmux pane.
- **Task detail modal** — a new key (e.g. `Space` or `Tab`) on a selected task opens a modal/overlay showing:
  - All context-sensitive actions for that task (park/unpark, freeze/wake, complete, companion, add repos, etc.), each with its keybinding
  - Task stats: attention state, cwd, session ID, creation time, session age, companion count, last hook event, summary
  - Background process list (from proctree) with full command lines
  - Token usage and estimated cost (if trackable from hook events)
  - Activity sparkline (larger than the inline version, if sparklines are implemented)
- Actions are invoked from within the modal via their existing keybindings. The modal closes after the action completes (or on `Esc`).

This reduces cognitive load on the main screen and gives a natural home for per-task details that don't fit in a table row.

### Merge Complete and Kill

Currently `complete` and `kill` are separate operations with only a semantic difference. Consolidate into a single action — `complete` is probably the right name since `kill` sounds violent and `close` implies reopenability. The underlying behavior (graceful shutdown, workspace cleanup) is the same regardless.

## UI Polish

- **Scrollable help with glossary** — replace the static help overlay with a scrollable viewport. Add a glossary section explaining concepts (companion windows, park/freeze, krang-parked session, attention states, etc.) so new users can understand the TUI without external docs. Use Bubble Tea's viewport for j/k scrolling with a scroll indicator.
- **Activity sparklines** — display a small time-series graph next to each task showing recent activity, color-coded by phase (thinking, tool calls, writing code, waiting for user, permission blocked). Requires storing timestamped activity events in the DB with a rolling retention window, and rendering sparkline-style characters (▁▂▃▄▅▆▇█) in the task list. Could use hook events already being captured to classify activity phases.
- **Fuzzy filter in repo picker** — Ctrl-F (or `/`) to enter a fuzzy search mode that narrows the repo picker list as you type. Useful when the repos directory has dozens of repos and scrolling through j/k is painful. Could reuse the existing `textinput` component from the task filter and apply fuzzy matching to both set names and repo names.
- **Tmux window number in # column** — Replace the current sequential row index with the actual tmux window number. This lets users mentally map table rows to `Ctrl-B <n>` for quick switching. Parked and frozen tasks would show a blank since they have no active window. Requires reading the window index from tmux (already available in the window name/target).

## ~~Hotkey Hint Placement~~ (Done)

Implemented: three-zone hint layout (table toolbar, action bar, footer) plus a command palette (`:`) for rare commands (sit rep, import, compact). No hidden hotkeys remain.

## Discoverability & Feedback

The app should make it obvious what's happening and what's about to happen. Several areas need work:

### Confirmations and Warnings

- **Completion warning** — when completing a task with a workspace, warn that the workspace directory will be deleted and show the path. Same for any destructive lifecycle transition.
- **Task creation preview** — the new task wizard should show what it's about to do: which repos will be cloned, where the workspace directory will be created, what sandbox will be used. Show this as a summary step before executing.

### Progress and Blocking Feedback

- **Freeze/complete should block or show a spinner** — currently these operations happen asynchronously and the task row doesn't update until the process closes (up to 5 seconds). The task appears unresponsive. Either block the UI with a spinner on that row or show an intermediate "freezing..." / "completing..." state so the user knows something is happening.
- **Workspace creation progress** — already partially implemented (workspace progress mode), but should show what's happening at each step (cloning repo X, setting up workspace at path Y).

### In-App Config Editor

- **Config form** — a TUI form (via `huh`) for editing both project-level (`krang.yaml`) and user-level (`config.json`) configuration. Avoids requiring users to hand-edit JSON/YAML files. Could be a modal accessible from the main screen or from the help overlay.

## Integration

- **Obsidian Kanban sync** — create tasks from Kanban cards, mark cards done when tasks complete

## Workspace Enhancements

Core workspace support (creation, cleanup, repo sets, add-repos, sandbox templating) is implemented. Remaining ideas:

- **Blank slate workspace** — The task creation wizard should support creating a task in a brand new empty directory that krang provisions. Useful for experiments and greenfield projects where you don't need an existing repo. Krang would create a temp directory (e.g. `~/.local/share/krang/workspaces/<task-name>/`), set it as the task's cwd, and clean it up on complete like any other workspace. The wizard could offer this as a "New directory" option alongside the existing repo/cwd pickers.

- **Workspace management API** — HTTP endpoints on krang's hook server (e.g. `POST /api/workspace/add-repo`) so Claude sessions can request workspace changes without the user switching to the TUI. A CLI subcommand (`krang workspace add-repo --task foo --repo bar`) reads `KRANG_STATEFILE` for the port and curls the API. A skill file in `.claude/commands/` tells Claude how to use the CLI. All mutations go through the HTTP server for serialization. See `docs/workspaces.md` for the original design sketch.

- **GitHub repo discovery** — Allow `krang.yaml` to specify GitHub orgs or accounts as repo sources. Claude (or the user via `W`) could search for repos via `gh api` and clone them into the repos directory on demand. Requires `gh` CLI with a valid auth token (`gh auth token`). This means the repos dir doesn't need to be pre-populated — you start with an empty repos dir and pull repos as needed. Could integrate with the repo picker as a "search GitHub" option alongside local repos.

## Sandbox Configuration

Currently krang supports a single `sandbox_command` string in config. This should evolve to support multiple sandboxing technologies — particularly Docker-based sandboxing alongside the existing command-line approach.

### Motivation

Some users (and teams) use Docker sandboxing for Claude Code, which requires pointing at a Dockerfile and potentially passing different flags than a CLI sandbox wrapper. Supporting both technologies lets users pick what fits their environment, and opens the door to per-task sandbox selection.

### Design Sketch

- **Replace `sandbox_command` with a richer config object** — something like:
  ```json
  {
    "sandboxes": {
      "bwrap": {
        "type": "command",
        "command": "bwrap --ro-bind / / ..."
      },
      "docker": {
        "type": "docker",
        "dockerfile": "~/.config/krang/sandbox/Dockerfile",
        "build_args": {},
        "run_args": ["--network=host"]
      }
    },
    "default_sandbox": "bwrap"
  }
  ```
- **Named sandboxes** — each sandbox config gets a name. One is marked as the default. The task creation form could offer a sandbox picker when multiple are configured.
- **Docker-specific concerns** — Dockerfile path, build caching, volume mounts for the working directory and krang state paths, env var passthrough (`KRANG_STATEFILE`, `KRANG_DEBUG`), and ensuring the relay script is accessible inside the container.
- **Backward compatibility** — if the old `sandbox_command` string is present, treat it as a single `"command"` type sandbox named `"default"`.

## Technical

- **Proper migration system** — versioned migrations with a schema_version table instead of idempotent DDL
- **Better error surfacing** — some operations fail silently; consider a dedicated error log file
- **Configurable models** — allow changing the summary (haiku) and sit rep (sonnet) models
