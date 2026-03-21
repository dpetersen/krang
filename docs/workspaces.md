# Workspace Management

## Context

Krang manages Claude Code sessions that often span multiple repos.
Today all tasks share krang's working directory. The workspace feature
creates isolated per-task directories populated with VCS-linked copies
of repos from a roots directory, giving each Claude session its own
working tree.

The feature is designed for gradual adoption:

1. **No config** — task creation prompts for a cwd (immediate children
   of krang's directory). Hit enter for krang's own cwd (today's
   behavior) or pick a subdirectory.
2. **`krang.yaml` present** — full workspace mode with repo sets,
   workspace creation from a roots directory, and automated cleanup.

## Directory Layout (Workspace Mode)

```
~/code/launchdarkly/               # metarepo root, krang runs here
├── roots/                         # all repo clones (source of truth)
│   ├── gonfalon/
│   ├── gonfalon-priv/
│   ├── terraform-config/
│   └── catfood/
├── auth-refactor/                 # workspace (created by krang)
│   ├── gonfalon/                  # jj workspace or git clone
│   └── gonfalon-priv/
├── krang.yaml                     # version-controlled workspace config
├── CLAUDE.md
└── .gitignore
```

## krang.yaml Format

Lives in the metarepo root, version-controlled. Its presence opts a
krang instance into workspace mode.

```yaml
roots_dir: roots  # relative to metarepo root, default "roots"

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
    - terraform-shared
  frontend:
    - gonfalon
    - catfood
```

**VCS auto-detection:** Check for `.jj/` in the root repo directory.
Present = jj, absent = git. The `repos` section is only needed to
override this when auto-detection gives the wrong answer.

**Repo deduplication:** When multiple sets are selected and share repos,
the resolved repo list is deduplicated.

## VCS Operations

### jj (workspace add)

```
cd ~/code/launchdarkly/roots/gonfalon
jj workspace add ../../auth-refactor/gonfalon --name auth-refactor
```

Creates a linked working copy. Shared object store, space-efficient.

### git (clone)

```
git clone ~/code/launchdarkly/roots/gonfalon ~/code/launchdarkly/auth-refactor/gonfalon
```

Full independent clone. Simple, no shared branch constraints.

## TUI Flow

Task creation adapts based on whether `krang.yaml` exists:

### Without krang.yaml — CWD Picker (ModeSelectCwd)

```
ModeNewName → ModeNewPrompt → ModeSelectCwd → create task
```

Lists immediate child directories of krang's cwd, plus `.` (krang's
own cwd) as the default selection:

```
Working directory for "fix-auth-bug":

> .  (current directory)
  project-a
  project-b
  project-c

j/k: navigate  enter: select
```

Enter selects the cwd and creates the task. Hitting enter on `.`
gives tier 1 behavior (today's default).

### With krang.yaml — Repo Selection (ModeRepoSelect)

```
ModeNewName → ModeNewPrompt → ModeRepoSelect → create task
```

Reuses the toggle-list pattern from ModeFlagEdit. Shows sets and
individual repos in a flat list:

```
Select repos for "auth-refactor":

> [x] backend (set)
  [x]   gonfalon
  [x]   gonfalon-priv
  [ ] terraform (set)
  [ ]   terraform-config
  [ ]   terraform-modules
  [ ]   terraform-shared
  [x]   catfood

j/k: navigate  space: toggle  enter: create  esc: cancel
```

- Toggling a set toggles all its members
- Individual repos can be toggled independently
- **Enter** with at least one selection: create workspace, then task
- **Enter** with nothing selected: does nothing (need at least one repo)
- **Esc**: cancels task creation entirely

## Task Lifecycle Integration

| Action   | Workspace behavior |
|----------|-------------------|
| Create   | Create workspace (if krang.yaml) or pick cwd |
| Park     | No change |
| Unpark   | No change |
| Freeze   | No change (preserve uncommitted work) |
| Wake     | No change (workspace dir still exists) |
| Complete | Destroy workspace |
| Kill     | Destroy workspace |
| Relaunch | No change |

### Workspace Destruction

1. For each repo dir in the workspace:
   - If jj: run `jj workspace forget <workspace-name>` from the
     corresponding root repo
   - If git: no VCS cleanup needed
2. `rm -rf` the workspace directory

## DB Changes

### Migration V5

```sql
ALTER TABLE tasks ADD COLUMN workspace_dir TEXT NOT NULL DEFAULT '';
```

Empty string = no workspace (backward compatible). Stores the absolute
path to the workspace directory.

### Task Struct

Add `WorkspaceDir string` to the `Task` struct. Add
`UpdateWorkspaceDir(id, dir string) error` to `TaskStore`.

## New Package: internal/workspace/

### reposets.go

```go
type RepoConfig struct {
    VCS string // "jj", "git", or "" (auto-detect)
}

type RepoSets struct {
    RootsDir string
    Repos    map[string]RepoConfig
    Sets     map[string][]string
}

func Load(metarepoDir string) (*RepoSets, error)
func (rs *RepoSets) ResolveRepos(sets, extras []string) []string
func (rs *RepoSets) DetectVCS(metarepoDir, repoName string) string
```

### workspace.go

```go
type CreateResult struct {
    WorkspaceDir string
    Repos        map[string]string // repo name → VCS used
    Errors       []string          // partial failure messages
}

func Create(metarepoDir, taskName string, rs *RepoSets, repos []string) (*CreateResult, error)
func Destroy(workspaceDir string) error
```

## Manager Changes

`Manager` gains a `metarepoDir` field (set from `os.Getwd()` at
startup). `Complete` and `Kill` call `workspace.Destroy` when
`task.WorkspaceDir` is non-empty.

The TUI handles workspace creation before calling `CreateTask`,
passing the workspace dir as the cwd. The manager doesn't need to
know about workspace creation, only cleanup.

## Adding Repos to an Existing Workspace

A keybinding on active/parked tasks (e.g., `W`) opens the repo
picker with already-included repos checked and disabled. The user
selects additional repos; on confirm, krang runs VCS operations for
just the new repos into the existing workspace directory. No schema
changes — `workspace_dir` is the same, the directory just gains new
subdirectories.

## Workspace Management API (Phase 2)

Extend krang so Claude sessions can request workspace changes
without the user switching to the krang TUI.

### Architecture

Three layers, each building on the previous:

1. **HTTP API on krang's server** — management endpoints alongside
   the existing hook routes (e.g., `POST /api/workspace/add-repo`).
   This is the single coordination point that prevents races when
   multiple Claude sessions try to modify workspaces concurrently.
   All mutations go through here.

2. **CLI subcommand** — `krang workspace add-repo --task foo --repo bar`
   reads `KRANG_STATEFILE` to find the port and curls the API. Works
   for humans, scripts, and Claude sessions.

3. **Skill** — a markdown file for `.claude/commands/` that tells
   Claude how to use the CLI. No MCP needed — Claude just runs the
   command. The skill lives in the krang repo and `krang setup` (or
   `krang install-skill`) copies it into the project's
   `.claude/commands/`, keeping skill and CLI versioned together.

### Why not MCP?

A skill calling a CLI calling an API achieves the same thing with
far less machinery. MCP requires a server process, protocol
handshake, and tool registration. The skill is a markdown file.
The CLI is a single subcommand. The API endpoint already exists.

### Concurrency

All workspace mutations go through krang's HTTP server, which
processes them sequentially. The CLI and skill are thin clients that
post to the API. Two Claude sessions asking for the same repo get
deduplicated naturally — the second add-repo call sees the repo
already exists in the workspace and returns success.

## Task Name Validation

Workspace directories use the task name, so names must be
filesystem-safe. Add validation that task names match
`[a-zA-Z0-9_-]+` (alphanumeric, hyphens, underscores).

## Edge Cases

- **Workspace dir already exists:** Error out. This means something
  unexpected is there — don't silently overwrite.
- **Partial creation failure:** Some repos succeed, others fail. Create
  the task with whatever succeeded, log the failures.
- **jj workspace name collision:** If a workspace named `<taskName>`
  already exists in a root repo (stale from a crash), forget it first,
  then recreate.
- **Cleanup failure:** Log but still transition the task to
  completed/failed. Stale workspace dirs are harmless.
- **Root repo missing:** If a root repo doesn't exist at create time,
  skip it and log the error.

## File Changes Summary

| File | Change |
|------|--------|
| `internal/workspace/reposets.go` | New: YAML parsing, repo resolution, VCS detection |
| `internal/workspace/workspace.go` | New: Create and Destroy functions |
| `internal/db/migrations.go` | Add schemaV5 for workspace_dir column |
| `internal/db/tasks.go` | Add WorkspaceDir field, UpdateWorkspaceDir method |
| `internal/task/manager.go` | Add metarepoDir field, cleanup in Complete/Kill |
| `internal/tui/model.go` | Add ModeSelectCwd and ModeRepoSelect, wire flows |
| `internal/tui/messages.go` | Add new mode constants |
| `internal/tui/view.go` | Add renderSelectCwd and renderRepoSelect methods |

## Implementation Order

### Phase 1: Core Workspaces

1. `internal/workspace/reposets.go` + tests
2. `internal/workspace/workspace.go` + tests
3. DB migration V5, Task struct update
4. Manager changes (metarepoDir, cleanup in Complete/Kill)
5. TUI: ModeSelectCwd (works without krang.yaml)
6. TUI: ModeRepoSelect (krang.yaml path)
7. Wire everything in cmd/root.go
8. Manual integration testing with real repos

### Phase 2: Add Repos to Existing Workspace

9. TUI: `W` keybinding, reuse ModeRepoSelect with pre-checked repos
10. `workspace.AddRepos()` function (subset of Create logic)

### Phase 3: Workspace Management API

11. HTTP API endpoints on krang's hook server
12. `krang workspace` CLI subcommand
13. Skill file + `krang install-skill` command
14. Integration testing with a live Claude session

## Deferred

- Repo selection memory (remember last picks)
- Workspace repair/status view
- Freeze-time workspace cleanup (risky with uncommitted work)
- Git branch cleanup on destroy
- CRUD UI for krang.yaml
