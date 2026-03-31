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

## Integration

- **Obsidian Kanban sync** — create tasks from Kanban cards, mark cards done when tasks complete

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

- **GitHub repo discovery enhancements** — core search/clone flow is implemented. Remaining ideas:
  - Shallow clones (`--depth 1`) as a configurable option for speed
  - Clone from a specific URL (not just org search) for repos outside discovered orgs
  - Auto-discover orgs via `gh api /user/orgs` instead of manual configuration

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

## Task Forking

Fork an existing task to branch off a new task with the same conversation history but an independent workspace. See [forking.md](forking.md) for the full design sketch covering Claude's `--fork-session` flag, the jj-vs-git workspace story, and open questions.

## Technical

- **Proper migration system** — versioned migrations with a schema_version table instead of idempotent DDL
- **Better error surfacing** — some operations fail silently; consider a dedicated error log file
- **Configurable models** — allow changing the summary (haiku) and sit rep (sonnet) models

## Discarded / Deferred Ideas

### Estimated cost tracking

Token usage display is implemented in the detail modal (parsing transcript JSONL files for per-API-call usage), but dollar cost estimation was dropped.

**What we found:**
- Claude Code hook events do **not** include token usage data. The hooks provide session IDs, event names, tool names, etc., but nothing about tokens or billing.
- The transcript JSONL files (`~/.claude/projects/<project>/<session-id>.jsonl`) **do** contain full Anthropic API usage on every `assistant` message: `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, plus the model ID.
- Transcripts write multiple entries per API response (streaming updates), so entries must be deduplicated by `message.id` to avoid double-counting.
- Subagent transcripts are stored separately in `<session-id>/subagents/*.jsonl` and contain only the subagent's messages (no overlap with the main transcript).

**Why cost estimation doesn't work well:**
- Enterprise pricing differs significantly from published API rates (~5x cheaper for Opus in at least one case), making hard-coded rates misleading.
- There's no programmatic API to query actual billing rates.
- The Claude Code `/cost` command shows accurate per-session costs, but that data isn't exposed via hooks or any external interface.

**Possible future path:** The `claude-cost` CLI plugin (`~/.claude/plugins/`) stores cost data somewhere locally. If that storage format can be reverse-engineered or if the plugin exposes an API, it could provide accurate cost data without hard-coding rates. Worth revisiting if the plugin ecosystem matures.
