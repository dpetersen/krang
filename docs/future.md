# Future Plans

## Sit Rep Enhancements

- **Parked sit rep variant** — a separate command to get briefings on parked tasks ("what should I do with these?")
- **Store `last_assistant_message`** from Stop hook payloads — gives cheaper context without reading full transcripts
- **Sit rep as modal overlay** — render on top of the existing TUI instead of replacing it (requires terminal compositing or a Bubble Tea overlay approach)

## Task Management

- **Task history view** — see completed/failed tasks with their final summaries, with the ability to revive them

## ~~tmux Window Naming~~ (done)

Implemented. Task windows are just `<name>`, companions are `<name>+`, identified via `@krang-task` and `@krang-companion` tmux user options. `@krang-attn` set on task windows for custom theme integration. Sessions shortened to `k-<instanceID>`, TUI window is `🧠`.

## UI Polish

- **Scrollable help with glossary** — replace the static help overlay with a scrollable viewport. Add a glossary section explaining concepts (companion windows, park/freeze, krang-parked session, attention states, etc.) so new users can understand the TUI without external docs. Use Bubble Tea's viewport for j/k scrolling with a scroll indicator.
- **Activity sparklines** — display a small time-series graph next to each task showing recent activity, color-coded by phase (thinking, tool calls, writing code, waiting for user, permission blocked). Requires storing timestamped activity events in the DB with a rolling retention window, and rendering sparkline-style characters (▁▂▃▄▅▆▇█) in the task list. Could use hook events already being captured to classify activity phases.
- **Fuzzy filter in repo picker** — Ctrl-F (or `/`) to enter a fuzzy search mode that narrows the repo picker list as you type. Useful when the repos directory has dozens of repos and scrolling through j/k is painful. Could reuse the existing `textinput` component from the task filter and apply fuzzy matching to both set names and repo names.

## Integration

- **Obsidian Kanban sync** — create tasks from Kanban cards, mark cards done when tasks complete

## Workspace Enhancements

Core workspace support (creation, cleanup, repo sets, add-repos, sandbox templating) is implemented. Remaining ideas:

- **Workspace management API** — HTTP endpoints on krang's hook server (e.g. `POST /api/workspace/add-repo`) so Claude sessions can request workspace changes without the user switching to the TUI. A CLI subcommand (`krang workspace add-repo --task foo --repo bar`) reads `KRANG_STATEFILE` for the port and curls the API. A skill file in `.claude/commands/` tells Claude how to use the CLI. All mutations go through the HTTP server for serialization. See `docs/workspaces.md` for the original design sketch.

- **GitHub repo discovery** — Allow `krang.yaml` to specify GitHub orgs or accounts as repo sources. Claude (or the user via `W`) could search for repos via `gh api` and clone them into the repos directory on demand. Requires `gh` CLI with a valid auth token (`gh auth token`). This means the repos dir doesn't need to be pre-populated — you start with an empty repos dir and pull repos as needed. Could integrate with the repo picker as a "search GitHub" option alongside local repos.

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
