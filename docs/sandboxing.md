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
