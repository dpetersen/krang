# Future Plans

## Sit Rep Enhancements

- **Parked sit rep variant** — a separate command to get briefings on parked tasks ("what should I do with these?")
- **Store `last_assistant_message`** from Stop hook payloads — gives cheaper context without reading full transcripts
- **Sit rep as modal overlay** — render on top of the existing TUI instead of replacing it (requires terminal compositing or a Bubble Tea overlay approach)

## Task Management

- **Task history view** — see completed/failed tasks with their final summaries, with the ability to revive them

## ~~Companion Windows~~ (Done)

- ~~**Create companion shortcut** — `+` keybinding creates a `KF!<task>` companion window adjacent to the task's `K!` window.~~

## ~~tmux Window Compaction~~ (Done)

- ~~**Compact windows command** — `C` keybinding renumbers all windows sequentially.~~

## UI Polish

- ~~**tmux window color coding** — set the tmux window/tab style to match attention state (red for permission requests, yellow for waiting, etc.) so the status bar itself signals which tasks need attention without switching to the krang TUI~~ (Done)
- **Scrollable help with glossary** — replace the static help overlay with a scrollable viewport. Add a glossary section explaining concepts (companion windows, park/freeze, krang-parked session, attention states, etc.) so new users can understand the TUI without external docs. Use Bubble Tea's viewport for j/k scrolling with a scroll indicator.
- **Activity sparklines** — display a small time-series graph next to each task showing recent activity, color-coded by phase (thinking, tool calls, writing code, waiting for user, permission blocked). Requires storing timestamped activity events in the DB with a rolling retention window, and rendering sparkline-style characters (▁▂▃▄▅▆▇█) in the task list. Could use hook events already being captured to classify activity phases.

## UI Rework & Theming

Full visual overhaul, best done after core features stabilize so the theme covers everything.

### Theming Infrastructure

- **Define a `Theme` struct** mapping semantic roles to colors: Title, Subtitle, Border, Selected, Muted, OK, Warning, Error, Done, Accent. All lipgloss styles derive from the active theme rather than hardcoded color numbers.
- **Refactor `styles.go`** — replace the 9 package-level style vars with a `buildStyles(theme Theme) Styles` function. No style should reference a raw color number directly.
- **Store theme selection** in `~/.config/krang/config.json` (already exists). Support `--theme` flag override.

### Bundled Themes

- **Catppuccin** (all 4 flavors: Latte, Frappe, Macchiato, Mocha) via the official [catppuccin/go](https://github.com/catppuccin/go) package, which provides 26 named colors per flavor in Hex/RGB/HSL.
- **Classic terminal themes** via [brittonhayes/glitter](https://github.com/brittonhayes/glitter) (Monokai, Gruvbox, Nord, Dracula) or [willyv3/gogh-themes/lipgloss](https://github.com/willyv3/gogh-themes) (361+ schemes) or [go.withmatt.com/themes](https://go.withmatt.com/themes) (450+ from iTerm2-Color-Schemes).
- Pick whichever library gives the best coverage-to-dependency ratio; avoid pulling in all 450 if only bundling a curated set.

### Layout & Component Upgrades

- **Replace custom table** with `bubbles/table` — handles column alignment, scrolling, and selection styling out of the box, eliminating manual `padRight` / ANSI-width workarounds.
- **Borders** — wrap the task list in `lipgloss.RoundedBorder()` for visual structure.
- **Status bar** — full-width colored strip at the bottom (like vim) instead of floating text.
- **Dim secondary info** — use `Faint(true)` for cwd, debug log, and other low-priority text instead of slightly-different gray shades.
- **Consistent spacing** — replace raw `\n\n` padding with lipgloss `Margin()` and `Padding()`.
- **Consider `huh`** for input flows (task creation, flag editing) — provides styled prompts, selects, and confirms with built-in Catppuccin support.

## Integration

- **Obsidian Kanban sync** — create tasks from Kanban cards, mark cards done when tasks complete
- **Multi-instance support** — see below

## Multi-Instance Support & Resilient Hooks

See [docs/multi-krang.md](multi-krang.md) for the full design.

## Workspace Management (Big Feature)

See [docs/workspaces.md](workspaces.md) for the full design.

## Process Tree Awareness

Surface background child processes per task in the TUI and feed that context into summaries and sit rep. These are the processes that tell you something the pane might not — like a `gh run watch` still running even though Claude looks idle.

### Data Collection

- **Extend `findClaudeChild`** (already in `task/manager.go`) to walk the full process tree instead of returning a single PID. From the shell PID, find the Claude process, then enumerate its children via `pgrep -P <claude_pid>`. Use `ps -eo pid,ppid,command -ww` to get the **full command line** with all args (the `-ww` flag prevents truncation).
- **Classify and describe children** by inspecting the command string:
  - `npm exec ...mcp-obsidian...` or similar = MCP servers (filter out, not "work")
  - `caffeinate` = internal keepalive (filter out)
  - Everything else = background tasks, and **the full command line is the description**. A `gh run watch 12345` or `npm test` or `kubectl wait` will be clearly visible in the args.
- **Sub-agents are invisible** — they run in-process within the node runtime, not as separate PIDs. Not worth tracking; their activity is already reflected in pane content and transcripts.
- **Collect full command strings**, not just counts:
  ```go
  type ChildProcess struct {
      PID     int
      Command string // full command line from ps -ww
  }
  ```
- **Walk recursively** — the tree is often shell → Claude → node → bash → actual command. Need to recurse to find the leaf commands that represent real work.
- **Poll on a tick** (every 3-5 seconds, same cadence as other TUI refreshes). Store transiently on the task model — no DB needed since this is ephemeral runtime state.

### TUI Display

- Show an indicator in the task row when background processes are running, e.g. `⚙3` (3 child processes).
- When a task is in `wait` state but has children running, this is the key signal: "Claude is idle but work is still happening." Could show as `wait⚙` or a distinct attention color to differentiate "genuinely waiting for you" from "waiting on background work."

### Summary Integration

- **Per-task summaries** (`summary/pipeline.go`): already captures 50 lines of pane content and sends to Haiku. Pass the full process list to the prompt, e.g. "Active child processes: 1 sub-agent (claude), 1 background task (gh run watch 12345678)." The command strings give Haiku enough to produce a meaningful one-liner like "Waiting on CI run #12345678 (gh run watch still running)."
- **Sit rep** (`summary/sitrep.go`): already includes attention state, pane content, and transcript path per task. Add a detailed process section per task:
  ```
  - Active child processes:
    - Sub-agent: claude (PID 78901)
    - Background: gh run watch 12345678 --exit-status (PID 78902)
  ```
  Sonnet can cross-reference this with the pane content and transcript to give a full picture (e.g. "Claude is in `wait` state, but a background `gh run watch` is still monitoring the CI pipeline for the FIPS compliance PR — no action needed until CI completes").

### Edge Cases

- **Process tree may be deep** — Claude spawns node, node spawns bash, bash spawns the actual command. Need to walk recursively or use `pgrep` with the right parent to avoid undercounting.
- **Short-lived processes** — a process might start and finish between polls. That's fine; the count is a snapshot, not a history. The sparklines feature (if built) would capture the temporal view.
- **Parked tasks** — still have tmux windows and running processes. Should collect process info for parked tasks too, since sit rep could cover them.

## Bugs

- **PERM attention state sticks after permission is resolved** — after approving or denying a permission prompt, the task stays in `PERM` attention state until the next `Stop` (wait) event. Likely no hook event fires between permission resolution and the next stop, so krang never learns Claude resumed. Need to investigate which hook events fire after a permission response and whether there's a gap to fill (e.g., a synthetic clear on any non-PermissionRequest event from the same task).

## Technical

- **Proper migration system** — versioned migrations with a schema_version table instead of idempotent DDL
- **Better error surfacing** — some operations fail silently; consider a dedicated error log file
- ~~**Configurable safehouse command** — done: `krang setup` prompts for sandbox command~~
- **Configurable models** — allow changing the summary (haiku) and sit rep (sonnet) models
