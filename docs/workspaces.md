# Workspace Management

## Context

Krang manages Claude Code sessions that often span multiple repos.
The workspace feature creates isolated per-task directories populated
with VCS-linked copies of repos, giving each Claude session its own
working tree.

The feature has three tiers of adoption:

1. **No `krang.yaml`** — task creation prompts for a cwd (immediate
   subdirectories of krang's directory). Select `.` for krang's own
   cwd (original behavior) or pick a subdirectory.
2. **`krang.yaml` with `workspace_strategy: single_repo`** — pick one
   repo, workspace dir is a direct clone.
3. **`krang.yaml` with `workspace_strategy: multi_repo`** — pick
   multiple repos (with optional set grouping), workspace dir
   contains clones.

## Directory Layout

### multi_repo

```
~/code/launchdarkly/               # metarepo root, krang runs here
├── repos/                         # source repos (configurable name)
│   ├── gonfalon/
│   ├── gonfalon-priv/
│   └── catfood/
├── workspaces/                    # workspaces (configurable name)
│   └── auth-refactor/             # named after task
│       ├── gonfalon/              # jj workspace or git clone
│       └── gonfalon-priv/
└── krang.yaml
```

### single_repo

```
~/code/launchdarkly/
├── repos/
│   └── gonfalon/
├── workspaces/
│   └── fix-auth/                  # IS the clone directly
│       ├── .git/
│       └── src/
└── krang.yaml
```

## krang.yaml

Lives in the metarepo root, version-controlled. The
`workspace_strategy` field is required to enable workspace mode —
without it, krang falls back to the CWD picker even if the file
exists.

```yaml
workspace_strategy: multi_repo  # or single_repo
repos_dir: repos                # default "repos"
workspaces_dir: workspaces      # default "workspaces"

repos:
  terraform-config:
    vcs: git      # override auto-detection
  # repos not listed here are auto-detected

sets:
  backend:
    - gonfalon
    - gonfalon-priv
  terraform:
    - terraform-config
    - terraform-modules
  frontend:
    - gonfalon
    - catfood
```

**VCS auto-detection:** Checks for `.jj/` in the repo directory.
Present = jj, absent = git. The `repos` map is only needed to
override auto-detection.

**Repo sets** (multi_repo only): Named groups of repos shown in
the repo picker as toggle-able headers. Toggling a set toggles all
its members. Individual repos always appear in the picker regardless
of set membership.

**Repo deduplication:** When sets overlap, the resolved repo list
is deduplicated.

## VCS Operations

### jj (workspace add)

```
cd ~/code/launchdarkly/repos/gonfalon
jj workspace add ../../workspaces/auth-refactor/gonfalon --name auth-refactor
```

Creates a linked working copy. Shared object store, space-efficient.

### git (local clone)

```
git clone ~/code/launchdarkly/repos/gonfalon ~/code/launchdarkly/workspaces/auth-refactor/gonfalon
```

Local clone uses hardlinks for the object store — nearly instant
and space-efficient. The working tree and branches are fully
independent.

## TUI Flow

### Without workspace_strategy — CWD Picker

Task creation form gains a third step (after name and flags) with
`huh.Select[string]` listing immediate subdirectories plus `.`
(current directory). Only shown when subdirectories exist.

### single_repo — Inline Select

Task creation form gains a third step with `huh.Select[string]`
listing repos from the repos directory. One repo, one clone.

### multi_repo — Custom Repo Picker

After the name+flags form completes, a custom toggle-list component
(`ModeRepoSelect`) shows sets and individual repos:

```
Select repos for "auth-refactor":

> [x] backend (gonfalon, gonfalon-priv)
  [x] gonfalon
  [x] gonfalon-priv
  [ ] terraform (terraform-config, terraform-modules)
  [ ] terraform-config
  [ ] terraform-modules
  [x] catfood

j/k: navigate  space: toggle  enter: create  esc: cancel
```

- Toggling a set toggles all its members
- Individual repos can be toggled independently
- Set checked state auto-syncs when individual repos change
- Enter with at least one selection creates the workspace
- Esc cancels task creation

### Adding Repos (W keybinding)

Press `W` on an active/parked multi_repo workspace task to add
repos. The repo picker shows only repos not already present in the
workspace. Uses the same VCS operations as initial creation.

### Progress Modal

Workspace creation and destruction show a bordered modal overlay
that blocks TUI input while operations run. Displays per-repo
status lines.

## Task Lifecycle Integration

| Action   | Workspace behavior |
|----------|-------------------|
| Create   | Create workspace or pick cwd |
| Park     | No change |
| Unpark   | No change |
| Freeze   | No change (preserve uncommitted work) |
| Wake     | No change (workspace dir still exists) |
| Complete | Destroy workspace (jj forget + rm -rf) |
| Kill     | Destroy workspace (jj forget + rm -rf) |
| Relaunch | No change |

### Workspace Destruction

1. For multi_repo: enumerate subdirectories; for each jj repo, run
   `jj workspace forget <workspace-name>` from the source repo.
   For single_repo: try `jj workspace forget` against all known
   jj repos.
2. `rm -rf` the workspace directory.
3. Errors are logged but don't block the state transition.

## Sandbox Integration

Workspace tasks launch Claude in a subdirectory of the metarepo.
Sandboxes (like safehouse) block reads above the workdir by default,
which breaks two things:

1. **Config file walking** — Claude walks upward looking for
   `.mcp.json`, `CLAUDE.md`, `.claude/` etc. Grant read access to
   these paths in the metarepo root via `{{.KrangDir}}`.

2. **VCS back-references** — jj workspaces and git worktrees are
   lightweight: they store a pointer back to the source repo's
   object store (`.jj/repo` or `.git/worktrees/`). Without access
   to the source repos directory, all VCS operations fail with
   "Operation not permitted". Grant read+write access to
   `{{.ReposDir}}` so tasks can read history and create commits.
   Plain `git clone` workspaces are self-contained and don't need
   this.

The `sandbox_command` config supports Go template variables:

| Variable | Value |
|----------|-------|
| `{{.KrangDir}}` | Krang's working directory (metarepo root) |
| `{{.TaskCwd}}` | Task's original launch cwd (stable, not live) |
| `{{.TaskName}}` | Task name |
| `{{.ReposDir}}` | Absolute path to repos directory (empty if no krang.yaml) |

Example granting config reads and full VCS access:

```json
{
  "sandbox_command": "safehouse --add-dirs-ro={{.KrangDir}}/.mcp.json:{{.KrangDir}}/CLAUDE.md:{{.KrangDir}}/.claude --add-dirs={{.ReposDir}} --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG"
}
```

If your repos are all plain git clones (not jj workspaces or git
worktrees), you can omit `--add-dirs={{.ReposDir}}`.

Falls back to the raw string on template parse errors.

## DB Schema

Migration V5 adds `workspace_dir TEXT NOT NULL DEFAULT ''` to the
tasks table. Empty string = no workspace (backward compatible).

## Packages

| Package | Key types/functions |
|---------|-------------------|
| `internal/workspace/reposets.go` | `RepoSets`, `Load()`, `ListRepos()`, `DetectVCS()`, `ResolveRepos()` |
| `internal/workspace/workspace.go` | `Create()`, `AddRepos()`, `Destroy()`, `PresentRepos()` |
| `internal/tui/repopicker.go` | `repoPicker` — custom toggle-list component |
| `internal/tui/forms.go` | `newWorkspaceTaskForm()` |

## Edge Cases

- **Workspace dir already exists:** Error. Don't silently overwrite.
- **Partial creation failure:** Create the task with whatever
  succeeded, log the failures.
- **All repos fail:** Clean up workspace dir, return error, no task
  created.
- **Cleanup failure:** Log but still transition the task to
  completed/failed. Stale workspace dirs are harmless.
- **No repos in repos dir:** Fall back to CWD picker.
- **Template parse error in sandbox command:** Fall back to raw
  string.
