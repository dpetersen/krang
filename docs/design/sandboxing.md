# Sandboxing

Krang supports running Claude Code inside a sandbox so that each task
has restricted filesystem and network access. This is optional — krang
works fine without any sandbox configured.

## Why Sandbox?

Claude Code can read and write files, run shell commands, and make
network requests. When you're running multiple concurrent Claude
sessions, sandboxing limits the blast radius of any single task.
A sandboxed task can only access the files and environment it needs,
not your entire home directory.

Krang itself always runs unsandboxed. Only the Claude processes inside
task windows are sandboxed.

## Named Profiles

Sandbox profiles are defined in `~/.config/krang/config.yaml` under
the `sandboxes` key. Each profile has a `type` field (currently only
`command` is supported) and type-specific fields:

```yaml
sandboxes:
  default:
    type: command
    command: safehouse --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG
  cloud-tools:
    type: command
    command: safehouse --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG --env-pass AWS_PROFILE
default_sandbox: default
```

With `type: command`, krang prepends the `command` string to the
`claude` invocation. So the example above runs:

```
safehouse --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG -- claude ...
```

Tasks can be assigned a specific profile at creation time or changed
later via the flag edit form (`F` in the detail modal). Changing the
profile on an active task triggers a relaunch.

Selecting "(none)" in the sandbox picker or not configuring any
profiles runs Claude unsandboxed.

## Requirements

Any sandbox tool you use must satisfy these requirements or krang's
hook events will silently fail:

### Environment Variables

| Variable | Required | Purpose |
|----------|----------|---------|
| `KRANG_STATEFILE` | Yes | Path to the port file that the relay script reads to find krang's HTTP server |
| `KRANG_DEBUG` | No | Enables relay script debug logging to `/tmp/krang-debug.log` |

### Filesystem Access

| Path | Access | Purpose |
|------|--------|---------|
| Task working directory | Read + Write | Claude needs to read and write the code it's working on |
| `~/.local/state/krang/` | Read | Relay script reads the state file for krang's port |
| `~/.config/krang/hooks/` | Read + Execute | Relay script lives here and must be executable |

If you're using [workspaces](workspaces.md), the sandbox also needs:

| Path | Access | Purpose |
|------|--------|---------|
| Repos directory | Read + Write | Both jj workspaces and git worktrees store a pointer back to the source repo's object store. Without access, all VCS operations fail |

The sandboxed Claude does **not** need access to the SQLite database
(`~/.local/share/krang/`) or write access to any krang paths. Krang
handles all DB writes from outside the sandbox.

### Template Variables

When using workspaces, the task's working directory is a subdirectory
of the metarepo, and Claude needs access to config files in the
metarepo root. Sandbox profiles of type `command` support Go template
variables to make this easy:

| Variable | Description |
|----------|-------------|
| `{{.KrangDir}}` | Krang's working directory (metarepo root) |
| `{{.TaskCwd}}` | Task's original launch cwd (does not drift) |
| `{{.TaskName}}` | Task name |
| `{{.ReposDir}}` | Absolute path to repos directory (empty if no krang.yaml) |

These are expanded at task launch time. Falls back to the raw string
on template parse errors.

## Safehouse

[Safehouse](https://github.com/nichochar/safehouse) is a macOS
sandbox wrapper that uses Apple's Seatbelt (`sandbox-exec`) to
restrict filesystem and network access. It's a good fit for krang
because it's lightweight, doesn't require root, and works with any
CLI tool.

### Basic Setup

Install safehouse, then configure krang to use it:

```yaml
sandboxes:
  default:
    type: command
    command: safehouse --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG
default_sandbox: default
```

This gives you safehouse's default restrictions plus the two env vars
krang needs.

### Granting Krang Access

Safehouse blocks access to paths outside the working directory by
default. Krang's relay script and state file live outside the task's
working directory, so you need to grant access. Create an override
profile at `~/.config/safehouse/claude-overrides.sb`:

```scheme
;; Krang: relay script reads state file, executes from hooks dir
(allow file-read* (subpath "~/.local/state/krang"))
(allow file-read* (subpath "~/.config/krang"))
(allow process-exec (subpath "~/.config/krang/hooks"))
```

Then reference it in your krang config:

```yaml
sandboxes:
  default:
    type: command
    command: safehouse --append-profile ~/.config/safehouse/claude-overrides.sb --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG
default_sandbox: default
```

### Workspace Setup

When using workspaces, Claude needs access to the metarepo root
(for config files like `CLAUDE.md` and `.mcp.json`) and the repos
directory (for VCS operations). Use template variables:

```yaml
sandboxes:
  default:
    type: command
    command: >-
      safehouse
      --append-profile ~/.config/safehouse/claude-overrides.sb
      --add-dirs-ro={{.KrangDir}}/.mcp.json:{{.KrangDir}}/CLAUDE.md:{{.KrangDir}}/.claude
      --add-dirs={{.ReposDir}}
      --env-pass KRANG_STATEFILE
      --env-pass KRANG_DEBUG
default_sandbox: default
```

The `--add-dirs-ro` grants read-only access to specific config files.
The `--add-dirs` grants read+write access to the repos directory,
which is needed because both jj workspaces and git worktrees reference
the source repo's object store.

### Workspace CLI Access

A sandboxed task that calls `krang workspace ...` itself — rather than
a human using the TUI — needs everything above plus four things.

**1. Permission to execute the krang binary.** `krang workspace` is a
subcommand of the krang binary, run as a plain process, not through the
relay script. This is a separate grant from the relay script's: follow
the same precedent (`process-exec` on the path the tool actually lives
at), pointed at krang instead:

```scheme
;; Krang: workspace CLI executes the krang binary itself. Use the path
;; `which krang` reports; SBPL literals do not expand ~, and a symlinked
;; install needs the RESOLVED target path, since Seatbelt matches the
;; real file.
(allow process-exec (literal "/Users/<you>/.local/bin/krang"))
;; Homebrew installs: (allow process-exec (literal "/opt/homebrew/bin/krang"))
```

If krang isn't installed via Homebrew, adjust the path — `which krang`
from an unsandboxed shell gives the path to grant.

**2. Loopback TCP to the hook server's dynamic port.** `krang workspace`
speaks HTTP to `127.0.0.1:<port>`, the same server the relay script
posts hook events to, just outbound instead of via the script's `curl`.
Safehouse's default policy is fully open to the network
(`(allow network*)`), so this works with no extra configuration out of
the box. If your profile has been narrowed past that default — safehouse
itself documents a "tight mode" that keeps only `network-outbound` open,
or narrower — grant outbound loopback access explicitly. The port is
dynamic per krang instance, so pin to the whole loopback range rather
than one port:

```scheme
;; Krang: workspace CLI reaches the hook server's dynamic loopback port
(allow network-outbound (remote ip "localhost:*"))
```

**3. `KRANG_STATEFILE` passthrough.** No new grant needed — it's the
same env var the relay script depends on (see Requirements above),
already covered by `--env-pass KRANG_STATEFILE` in the sandbox command.
It's exported into the shell before the sandbox tool runs
(`buildClaudeCommand` in `internal/task/manager.go`), so it's part of
the sandboxed process's environment from the moment it starts, and
every process Claude spawns from inside the sandbox — including a
`krang workspace` call from a Bash tool call — inherits it the ordinary
way, with nothing workspace-specific to configure.

**4. No relaunch for slots created after launch.** New slot directories
land inside the task's own working directory, which the sandbox already
grants read+write access to (see Requirements above). Because that
grant covers the whole working directory rather than the specific
subdirectories that existed at launch time, an in-flight sandboxed
session should be able to use a slot that `krang workspace add` creates
mid-session with no sandbox relaunch. This is the designed property,
not yet confirmed live in a sandboxed session — that verification is
tracked separately.

### Multiple Profiles

You can define multiple profiles for tasks with different access
needs:

```yaml
sandboxes:
  default:
    type: command
    command: safehouse --append-profile ~/.config/safehouse/claude-overrides.sb --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG
  cloud-tools:
    type: command
    command: safehouse --append-profile ~/.config/safehouse/claude-overrides.sb --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG --env-pass AWS_PROFILE --env-pass AWS_REGION
default_sandbox: default
```

A task that needs to run `aws` CLI commands gets the `cloud-tools`
profile; everything else gets `default`. You pick the profile at
task creation time or change it later via `F` in the detail modal.

### Troubleshooting

If hook events aren't showing up in krang (tasks stay in "ok" state,
no sparkline activity), the sandbox is the most likely cause. Enable
the Debug flag on a task and check `/tmp/krang-debug.log`:

- **No log entries at all** — the relay script can't execute. Check
  that `process-exec` is granted for `~/.config/krang/hooks/`.
- **"Permission denied" reading state file** — grant `file-read*`
  for `~/.local/state/krang/`.
- **Log entries but krang doesn't react** — the relay script is
  running but can't reach krang's HTTP server. This usually means
  `KRANG_STATEFILE` isn't being passed through.

`krang workspace` fails differently than a stuck hook event — it's a
foreground command, so it prints its own error instead of failing
silently. Its exit code says whether the request is safe to retry
(see `internal/workspaceclient/exit.go`'s `ExitCodeHelp`).

- **"exec denied" running `krang workspace`** — the krang binary
  itself isn't granted `process-exec`. This is a separate grant from
  the relay script's `~/.config/krang/hooks/` one, since `krang
  workspace` runs the krang binary directly. Add the grant from
  Workspace CLI Access above.
- **"connection refused" from `krang workspace`** — the CLI's own
  error names the loopback address and the state file it read it from,
  and exits 4 (`ExitUnavailable`). Means the TUI isn't running, or the
  state file names a port from an instance that's since exited. Start
  krang (or confirm this task's instance is still alive) and retry —
  exit 4 means nothing was applied, so retrying is safe.
- **404 on `/api/workspace`** — the running instance predates the
  workspace API, so its router answers "no such route" before any
  handler runs; nothing was applied. Quit and relaunch krang from a
  build that has the endpoint. The CLI names this explicitly and exits
  1 (`ExitError`), not the "maybe happened" exit 3, because a 404 is a
  hard guarantee that nothing ran.
