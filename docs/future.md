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

- **Workspace management API** — HTTP endpoints on krang's hook server (e.g. `POST /api/workspace/add-repo`) so Claude sessions can request workspace changes without the user switching to the TUI. A CLI subcommand (`krang workspace add-repo --task foo --repo bar`) reads `KRANG_STATEFILE` for the port and curls the API. A skill file in `.claude/commands/` tells Claude how to use the CLI. All mutations go through the HTTP server for serialization.

## Sandbox Configuration

Named sandbox profiles with a `type` discriminator are implemented (currently only `command` type). Remaining work:

- **Docker sandbox type** — Dockerfile path, build caching, volume mounts, env var passthrough. The `type: docker` schema would add `dockerfile`, `build_args`, `run_args` fields.
- **SafeHouse-specific type** — additive profile configs that could enable multi-select ("this task needs Kubernetes AND AWS").

## Task Forking

Task forking is implemented with two workspace modes (independent and shared). See [forking.md](design/forking.md) for details. Remaining ideas:

- **Linked mode** — jj parent-child workspace (`workspace add -r @`) for auto-rebase from source. Currently blocked by jj's stale workspace handling losing working copy changes on concurrent edits (jj-vcs/jj#1310). Worth revisiting if jj improves this.
- **Fork from completed tasks** — conversation-only fork (no workspace to copy). Would need session files to still be available.

## Multi-Agent Support (Pi, etc.)

Krang currently assumes Claude Code as the agent runner, but the architecture
could support other coding agents. The main integration point is the hook
event system — any agent that can report lifecycle events (session start/stop,
tool use, permission requests) to krang's HTTP server would work.

**Pi (badlogic/pi-mono):**

Pi has a rich event system but no shell-command hooks like Claude Code. The
most practical integration path is a **TypeScript extension** that lives in
Pi's extension directory (`~/.pi/agent/extensions/` or `.pi/extensions/`)
and POSTs events to krang's HTTP server, mirroring the relay script pattern.

Pi's events map well to krang's hook events:

| Pi Event | Krang Equivalent |
|---|---|
| `session_start` / `session_shutdown` | `SessionStart` / `SessionEnd` |
| `tool_call` (can block) | `PermissionRequest` / `PreToolUse` |
| `tool_execution_start/end` | `PreToolUse` / `PostToolUse` |
| `tool_result` | `PostToolUse` |
| `input` | `UserPromptSubmit` |
| `turn_start` / `turn_end` | (no direct equivalent) |

Pi also has an RPC mode (`pi --mode rpc`) with JSONL over stdin/stdout, but
the extension approach requires fewer changes to krang's architecture since
it preserves the "agent runs in a tmux window, events arrive via HTTP" model.

**Generalization work needed:**

- Abstract the hook event types so they're not Claude-specific
- Make the relay script / extension installable per-agent
- Agent-specific launch commands (already partially handled by sandbox
  profiles, but the base command itself needs to be configurable)
- Summary and classification prompts may need agent-specific tuning

## Discarded / Deferred Ideas

### Cost tracking

Previously attempted via both hardcoded per-model pricing and
[ccusage](https://github.com/ryoppippi/ccusage). Both approaches
were dropped because they rely on published API pricing, which
doesn't reflect enterprise contract rates. Revisit if Claude Code
hook events gain token usage data or if an accurate cost source
becomes available.
