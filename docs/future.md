# Future Plans

## Sit Rep Enhancements

- **Parked sit rep variant** — a separate command to get briefings on parked tasks ("what should I do with these?")
- **Store `last_assistant_message`** from Stop hook payloads — gives cheaper context without reading full transcripts
- **Sit rep as modal overlay** — render on top of the existing TUI instead of replacing it (requires terminal compositing or a Bubble Tea overlay approach)

## Task Management

- **Bulk operations** — park/freeze all, unpark all, etc.
- **Task notes** — attach freeform notes to tasks for your own context
- **Task history view** — see completed/failed tasks, maybe with their final summaries
- **Auto-freeze idle tasks** — after N minutes of no activity, offer to freeze

## Companion Windows

- **Create companion shortcut** — TUI keybinding to create a `KF!<task>` companion window for the selected task. The new window should be inserted immediately to the right of the task's `K!` window in tmux, pushing other windows over if needed.

## tmux Window Compaction

- **Compact windows command** — renumber all tmux windows in the active session so they're sequential (0, 1, 2, 3...) with no gaps. Windows tend to accumulate high numbers (10, 11, etc.) as tasks come and go. Could be a TUI keybinding or happen automatically on certain operations. tmux has `move-window -r` which renumbers sequentially.

## UI Polish

- **Notification sound/bell** — terminal bell when attention changes to permission or waiting
- **macOS notifications** — via `terminal-notifier` for attention changes when not looking at krang
- **Configurable keybindings**
- **Color themes**

## Integration

- **Obsidian Kanban sync** — create tasks from Kanban cards, mark cards done when tasks complete
- **Multi-instance support** — if ever needed, could namespace the port and DB path

## Workspace Management (Big Feature)

Krang always runs from the LaunchDarkly directory (`~/code/launchdarkly`), which contains all cloned repos. The goal is to automate setting up jj workspaces for new units of work.

### Concept

- **Roots directory** — a subdirectory containing every repo cloned in its default state. The user doesn't work directly in roots; it's the source of truth for creating workspaces.
- **Repo sets** — named configurations (stored in a config file) that define groups of repos needed for a type of work. Examples:
  - `terraform` → `terraform-config`, `terraform-modules`, `terraform-shared`
  - `backend` → `gonfalon`, `gonfalon-priv`, `ld-relay`
  - `frontend` → `gonfalon`, `catfood`
- **Task workspace** — when creating a new task, the user selects one or more repo sets. Krang creates a new directory named after the task (e.g., `~/code/launchdarkly/auth-refactor/`) and creates jj workspaces within it for each repo from the selected sets. The result is a flat list of repo workspaces ready to work in.
- **Lifecycle integration** — the task's cwd would be this workspace directory. Freezing/completing a task could optionally clean up the workspaces. Thawing would recreate them.

### Design Questions

- Where does the repo set config live? (`~/.config/krang/reposets.toml`? In the repo?)
- How to handle repos that appear in multiple sets? (deduplicate)
- Should workspace creation be part of `n` (new task) or a separate command?
- What happens to uncommitted work in workspaces when a task is frozen? (jj workspaces preserve state, but the user should be warned)

## Technical

- **Proper migration system** — versioned migrations with a schema_version table instead of idempotent DDL
- **Better error surfacing** — some operations fail silently; consider a dedicated error log file
- **Configurable safehouse command** — not everyone uses safehouse; make the Claude wrapper configurable
- **Configurable models** — allow changing the summary (haiku) and sit rep (sonnet) models
