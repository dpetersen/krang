# Future Plans

## Sit Rep Enhancements

- **Parked sit rep variant** — a separate command to get briefings on parked tasks ("what should I do with these?")
- **Store `last_assistant_message`** from Stop hook payloads — gives cheaper context without reading full transcripts
- **Sit rep as modal overlay** — render on top of the existing TUI instead of replacing it (requires terminal compositing or a Bubble Tea overlay approach)

## Task Management

- **Task history view** — see completed/failed tasks with their final summaries, with the ability to revive them

## Detail Modal Enhancements

The detail modal is implemented with context-sensitive actions, task stats, and background process display. Remaining additions:

- **Last hook event** — show the most recent hook event name/timestamp
- **Token usage and estimated cost** — track from hook events if possible

## Help Glossary & Scroll Indicator

Scrollable help with j/k navigation is implemented. Remaining work:

- **Scroll position indicator** — show a visual indicator of scroll position (percentage or position bar)
- **Expand glossary** — add explanations for concepts not covered elsewhere: workspaces and cleanup on complete, task flags, sandbox

## Smart Attention Classification

Currently the `Stop` hook event always maps to `AttentionWaiting`, but "Claude finished a task" and "Claude is asking you a question" feel very different. The yellow "wait" indicator can feel urgent when Claude is just done and idle.

### How it would work

The `Stop` hook payload includes `last_assistant_message` — Claude's final response text. A Haiku call (~350 input tokens, ~5 output tokens) can classify it as "done" (completed work, waiting for next instruction) vs "question" (asking the user something, needs a response). This maps to two distinct attention states with different visual treatments — e.g. a calm "done" vs an attention-grabbing "wait".

### Implementation

- **Config option** in `config.json`: `"classify_attention": true` (default off)
- **On `Stop` event**: if enabled, fire an async Haiku call with the `last_assistant_message`. The task stays `AttentionWaiting` immediately, then a Bubble Tea message updates it to `AttentionDone` or keeps it `AttentionWaiting` when the classification returns.
- **Nonblocking**: the classification runs in a goroutine, same pattern as the summary pipeline. The attention column updates when the result arrives (~500ms-1s later). No UI blocking.
- **Cost**: ~$0.000375 per classification. Even at 100 stops/day across all tasks, that's ~$0.04/day.
- **Existing infrastructure**: `AttentionDone` already exists in the DB, theme, and rendering code (currently only reachable via the `TaskCompleted` hook, which doesn't fire in standard single-agent sessions). The `last_assistant_message` field is already parsed from hook payloads.
- **Fallback**: if the Haiku call fails or times out, keep `AttentionWaiting` — no change from current behavior.

## Discoverability & Feedback

- **Freeze confirmation** — warn about companion window destruction before freezing a task that has companions
- **Task creation preview** — the new task wizard should show what it's about to do: which repos will be cloned, where the workspace directory will be created, what sandbox will be used. Show this as a summary step before executing.
- **Workspace creation progress** — already partially implemented (workspace progress mode), but should show what's happening at each step (cloning repo X, setting up workspace at path Y).
- **In-app config editor** — a TUI form (via `huh`) for editing both project-level (`krang.yaml`) and user-level (`config.json`) configuration. Avoids requiring users to hand-edit JSON/YAML files. Could be a modal accessible from the main screen or from the help overlay.

## Integration

- **Obsidian Kanban sync** — create tasks from Kanban cards, mark cards done when tasks complete

## Workspace Enhancements

Core workspace support (creation, cleanup, repo sets, add-repos, sandbox templating) is implemented. Remaining ideas:

- **Blank slate workspace** — The task creation wizard should support creating a task in a brand new empty directory that krang provisions. Useful for experiments and greenfield projects where you don't need an existing repo. Krang would create a temp directory (e.g. `~/.local/share/krang/workspaces/<task-name>/`), set it as the task's cwd, and clean it up on complete like any other workspace. The wizard could offer this as a "New directory" option alongside the existing repo/cwd pickers.

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
- **Consolidate on YAML config format** — user-level config (`config.json`) and project-level config (`krang.yaml`) use different formats. Migrate to YAML everywhere for consistency.
