# Future Plans

## Sit Rep Enhancements

- **Parked sit rep variant** — a separate command to get briefings on parked tasks ("what should I do with these?")
- **Sit rep as modal overlay** — render on top of the existing TUI instead of replacing it (requires terminal compositing or a Bubble Tea overlay approach)

## Task Management

- **Task history view** — see completed/failed tasks with their final summaries, with the ability to revive them

## Detail Modal Enhancements

The detail modal is implemented with context-sensitive actions, task stats, and background process display. Remaining additions:

- **Last hook event** — show the most recent hook event name/timestamp

## Help Glossary & Scroll Indicator

Scrollable help with j/k navigation is implemented. Remaining work:

- **Scroll position indicator** — show a visual indicator of scroll position (percentage or position bar)
- **Expand glossary** — add explanations for concepts not covered elsewhere: workspaces and cleanup on complete, task flags, sandbox

## Discoverability & Feedback

- **In-app config editor** — a TUI form (via `huh`) for editing both project-level (`krang.yaml`) and user-level (`config.yaml`) configuration. Avoids requiring users to hand-edit JSON/YAML files. Could be a modal accessible from the main screen or from the help overlay.

## MCP Read API

Expose krang's task state to external agents (e.g. a workload manager Claude that reads a Kanban/JIRA board) via read-only MCP resources or tools. The goal is not to build project management into krang, but to let agents query krang for situational awareness.

**Core data to expose:**

- **Task list with status** — name, state (active/parked/frozen), attention state, summary text from the DB. Essentially the data backing the TUI table without requiring the TUI.
- **Per-task detail** — summary, cwd, flags, workspace dir, age, last hook event. The same info the detail modal shows.

**Optional / configurable:**

- **Tmux pane contents** — capture recent output from task panes via `tmux capture-pane`. Useful for agents that want to check on task progress directly. Default off since pane contents can be large and noisy. Could offer two modes: raw capture, or just session/window/pane identifiers so the caller can capture themselves.

**Design notes:**

- All reads come from the SQLite DB and tmux queries — no spinning up Haiku calls or doing expensive work on behalf of callers.
- The existing hook HTTP server could host MCP endpoints, or this could be a separate MCP server process that reads the same DB.
- Start with tools (`get_tasks`, `get_task_detail`) rather than resources, since the data changes frequently.

## Workspace Enhancements

Core workspace support (creation, cleanup, repo sets, add-repos, sandbox templating) is implemented. Remaining ideas:

- **Workspace management API** — HTTP endpoints on krang's hook server (e.g. `POST /api/workspace/add-repo`) so Claude sessions can request workspace changes without the user switching to the TUI. A CLI subcommand (`krang workspace add-repo --task foo --repo bar`) reads `KRANG_STATEFILE` for the port and curls the API. A skill file in `.claude/commands/` tells Claude how to use the CLI. All mutations go through the HTTP server for serialization. See `docs/workspaces.md` for the original design sketch.

## Sandbox Configuration

Named sandbox profiles with a `type` discriminator are implemented (currently only `command` type). Remaining work:

- **Docker sandbox type** — Dockerfile path, build caching, volume mounts, env var passthrough. The `type: docker` schema would add `dockerfile`, `build_args`, `run_args` fields.
- **SafeHouse-specific type** — additive profile configs that could enable multi-select ("this task needs Kubernetes AND AWS").

## Task Forking

Task forking is implemented with two workspace modes (independent and shared). See [forking.md](forking.md) for details. Remaining ideas:

- **Linked mode** — jj parent-child workspace (`workspace add -r @`) for auto-rebase from source. Currently blocked by jj's stale workspace handling losing working copy changes on concurrent edits (jj-vcs/jj#1310). Worth revisiting if jj improves this.
- **Fork from completed tasks** — conversation-only fork (no workspace to copy). Would need session files to still be available.

## Discarded / Deferred Ideas

### Cost tracking via ccusage

Previously attempted with hardcoded per-model pricing, which was dropped because enterprise pricing differs significantly from published API rates.

Now delegated to [ccusage](https://github.com/ryoppippi/ccusage) via `npx`. The detail modal shows per-session cost when npx is available. The ccusage version is pinned in the binary (`ccusage.DefaultVersion`) and can be overridden per-user via `ccusage_version` in config.yaml.

**Background:**
- Claude Code hook events do **not** include token usage data.
- Transcript JSONL files contain full API usage but not dollar costs.
- ccusage reads these same transcripts and applies accurate pricing.
