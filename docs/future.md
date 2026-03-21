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

## Bugs

- **PERM attention state sticks after permission is resolved** — after approving or denying a permission prompt, the task stays in `PERM` attention state until the next `Stop` (wait) event. Likely no hook event fires between permission resolution and the next stop, so krang never learns Claude resumed. Need to investigate which hook events fire after a permission response and whether there's a gap to fill (e.g., a synthetic clear on any non-PermissionRequest event from the same task).

## Technical

- **Proper migration system** — versioned migrations with a schema_version table instead of idempotent DDL
- **Better error surfacing** — some operations fail silently; consider a dedicated error log file
- ~~**Configurable safehouse command** — done: `krang setup` prompts for sandbox command~~
- **Configurable models** — allow changing the summary (haiku) and sit rep (sonnet) models
