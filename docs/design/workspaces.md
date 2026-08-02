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
~/code/myproject/                  # metarepo root, krang runs here
├── repos/                         # source repos (configurable name)
│   ├── api-server/
│   ├── web-app/
│   └── payments/
├── workspaces/                    # workspaces (configurable name)
│   └── auth-refactor/             # named after task
│       ├── api-server/            # initial working copy — <repo>
│       ├── api-server--tests/     # slot — <repo>--<label>
│       ├── api-server--2/         # slot — auto-numbered label
│       └── web-app/
└── krang.yaml
```

A task's workspace directory is a *container* of working copies, and a
task may hold more than one working copy of the same repo. The first one
krang makes for a repo is the **initial** working copy and is named after
the repo; every one after it is a **slot** and carries a label in its
directory name. See [Slots](#slots) for how the names are derived.

### single_repo

```
~/code/myproject/
├── repos/
│   └── api-server/
├── workspaces/
│   └── fix-auth/                  # IS the clone directly
│       ├── .git/
│       └── src/
└── krang.yaml
```

`single_repo` sits outside the slot system. The workspace directory *is*
the working copy, so there is nowhere to put a second one, and the
subdirectories under it are that repo's own contents rather than
checkouts. Nothing scans them, and the removal API refuses to remove the
root (`workspace_root`) because doing so would be a task teardown wearing
a slot removal's clothes.

## krang.yaml

Lives in the metarepo root, version-controlled. The
`workspace_strategy` field is required to enable workspace mode —
without it, krang falls back to the CWD picker even if the file
exists.

```yaml
workspace_strategy: multi_repo  # or single_repo
repos_dir: repos                # default "repos"
workspaces_dir: workspaces      # default "workspaces"
default_vcs: jj                 # "git" (default) or "jj" — fallback for repos without .jj/

github_orgs:                    # orgs for GitHub repo discovery (merged with config.yaml)
  - myorg
  - other-org

sets:
  backend:
    - api-server
    - web-app
  terraform:
    - terraform-config
    - terraform-modules
  frontend:
    - api-server
    - payments
```

**VCS auto-detection:** Probes the repo directory for `.jj/` (returns
"jj") or `.git` (returns "git"), then falls back to `default_vcs`, then
"git". The `.git` check handles both directories (normal clones) and
files (worktrees/submodules). `default_vcs` and `github_orgs` can also
be set in `config.yaml` (user-level); the workspace config takes
precedence for `default_vcs`, and orgs are merged with dedup.

**Repo sets** (multi_repo only): Named groups of repos shown in
the repo picker as toggle-able headers. Toggling a set toggles all
its members. Individual repos always appear in the picker regardless
of set membership.

**Repo deduplication:** When sets overlap, the resolved repo list
is deduplicated.

## Slots

Every working copy krang creates goes through one path
(`workspace.CloneRepoAs`) under a `SlotIdentity`, which is the single
source of all three names the working copy needs. Nothing else derives
them.

```go
type SlotIdentity struct {
    TaskName string
    RepoName string
    Label    string  // "" means the task's initial working copy
    Base     string  // revset/commit-ish; takes no part in any name
}
```

### Naming scheme

| Derived name | Empty label (initial) | Non-empty label (slot) |
|---|---|---|
| `DirName()` — directory under the workspace dir | `<repo>` | `<repo>--<label>` |
| `VCSName()` — jj workspace name | `<task>` | `<task>--<repo>--<label>` |
| `GitBranch()` — git branch in the source repo | `krang/<task>` | `krang/<task>--<repo>--<label>` |

Worked example, task `auth-refactor`, repo `api-server`, label `tests`:

| | Directory | jj workspace | git branch |
|---|---|---|---|
| initial | `api-server` | `auth-refactor` | `krang/auth-refactor` |
| slot | `api-server--tests` | `auth-refactor--api-server--tests` | `krang/auth-refactor--api-server--tests` |

**Initial slots keep the legacy names on purpose.** An empty label means
the working copy predates — or would have predated — the slot system, so
it gets the names krang has always used. Nothing on disk is renamed and
no jj workspace or git branch created before slots existed has to be
migrated.

**Why the jj name carries the repo.** jj workspace names are unique per
*source repo*, not per task. Two different tasks may both label a slot
`tests`, and both labels land in the same source repo's workspace list;
including the repo and the task keeps them apart. The `(repo_name,
vcs_name)` unique constraint on `workspace_repos` is the same invariant
written down in SQL.

**Labels** are lowercase alphanumerics with single dashes
(`ValidateSlotLabel`, `^[a-z0-9]+(-[a-z0-9]+)*$`). `--` is reserved as
the separator so any derived name can be split back apart —
`ParseSlotDirName` relies on it to read a repo and label out of a
directory name when no provenance row exists.

**Auto-numbering.** `ResolveSlotIdentity` picks the first free
discriminator (`2`, `3`, …) when the caller gives no label. That is fine
for the human's repo picker, where the result is visible. The HTTP API
refuses instead (`label_required`) and suggests a free label, because an
agent handed `api-server--2` unasked cannot tell its checkouts apart.

**Refusal, not overwrite.** Before anything is written, the computed jj
workspace name is checked against `jj workspace list` in the source repo,
the computed git branch against `git branch --list`, and a computed slot
directory that would spell a managed repo's name is rejected outright.
The one reclaim krang still does is a leftover `krang/<task>` branch on
task-name reuse, and only when `git branch -d` agrees nothing is lost —
cleanup goes out of its way to keep unpushed branches, so creation must
not force-delete them.

**No cap.** A task may hold as many working copies as it asks for. An
earlier flat limit of four counted the initial repos too, so a four-repo
task was at the limit before any agent requested anything. Sprawl is
made visible instead of refused: the status line names every
API-initiated mutation as it happens and the detail modal lists every
working copy the workspace holds.

## VCS Operations

### jj (workspace add)

```
cd ~/code/myproject/repos/api-server

# initial working copy
jj workspace add ../../workspaces/auth-refactor/api-server \
  --name auth-refactor -r <base>

# a second slot of the same repo
jj workspace add ../../workspaces/auth-refactor/api-server--tests \
  --name auth-refactor--api-server--tests -r <base>
```

Creates a linked working copy. Shared object store, space-efficient.

### git (worktree add)

```
cd ~/code/myproject/repos/api-server

# initial working copy
git worktree add ../../workspaces/auth-refactor/api-server \
  -b krang/auth-refactor <base>

# a second slot of the same repo
git worktree add ../../workspaces/auth-refactor/api-server--tests \
  -b krang/auth-refactor--api-server--tests <base>
```

Creates a git worktree (lightweight linked working copy). Shared
object store, no file copying. The branch is namespaced under
`krang/` so it's clearly identifiable for cleanup.

**Base.** Slots are always created from the canonical repo under
`repos_dir`, never from a sibling working copy: branching off a neighbour
would inherit its in-progress state and tie the new slot's lifetime to
the neighbour's VCS identity. `<base>` is the caller's `--base` when it
named one, otherwise the detected remote default branch. Whichever it is,
the *effective* value — the one actually handed to the VCS — is what
lands in `base_revision`, because recording a bookmark name after the
fact points wherever the bookmark has since moved.

### .worktreeinclude

Git worktrees don't include gitignored files (like `.env`). Create
a `.worktreeinclude` file in your source repo root listing patterns
(gitignore syntax) of gitignored files to copy into new worktrees:

```
.env
.env.local
config/secrets.json
```

This matches Claude Code's built-in `.worktreeinclude` behavior.

## TUI Flow

### Without workspace_strategy — CWD Picker

Task creation form gains a third step (after name and flags) with
`huh.Select[string]` listing immediate subdirectories plus `.`
(current directory). Only shown when subdirectories exist.

### single_repo — Inline Select

Task creation form gains a third step with `huh.Select[string]`
listing repos from the repos directory. One repo, one clone.

### multi_repo — Tabbed Repo Picker

After the name+flags form completes, a tabbed repo picker opens
(`ModeRepoSelect`) with two tabs toggled via `Tab`:

**Local tab** — sets and individual repos from the repos directory:

```
Select repos for "auth-refactor":

  Local   Remote

> [x] backend (api-server, web-app)
  [x] api-server
  [x] web-app
  [ ] terraform-config
  [x] payments

tab switch tab  j/k navigate  space toggle  enter create  esc cancel
```

- Toggling a set toggles all its members
- Individual repos can be toggled independently
- Set checked state auto-syncs when individual repos change
- Enter with at least one selection creates the workspace
- Esc cancels task creation

**Remote tab** — search GitHub orgs and clone repos:

- If `github_orgs` is configured (in `config.yaml` or `krang.yaml`),
  shows an org select list with an "Other..." option for manual entry
- If no orgs configured, shows a text input for the org name
- After selecting an org, a debounced search input (300ms) queries
  GitHub via `gh api` and shows results as a single-select list
- Enter on a result clones it to the repos dir using `default_vcs`
  (git or jj), then returns to the Local tab with the new repo visible
- Requires `gh` CLI installed and authenticated; shows a message if unavailable

### Adding Repos (W keybinding)

Press `W` on an active/parked multi_repo workspace task to add
repos. The tabbed picker opens with the Local tab showing only repos
not already present in the workspace. The Remote tab can clone new
repos from GitHub. Uses the same VCS operations as initial creation.

The "already present" filter is slot-aware: it hides the *repos*
(`PresentRepos`) a workspace holds rather than the directory names, so a
second slot of a repo doesn't read as an unknown repo of its own. The
picker is how a human adds a repo the task doesn't have; adding a second
checkout of a repo it already has is an API-side operation
(`POST /api/workspace/add` with a `label`, or `krang workspace add`).

### Progress Modal

Workspace creation and destruction render as centered modal overlays
(2/3 terminal width) using `overlayCenter()`. Each repo clone or
forget is a separate `tea.Cmd`, so the UI updates between operations.

**Creation progress** shows:
- Per-repo checklist with status icons: `·` pending, spinner active,
  `✓` done, `✗` failed. A `[done/total]` counter on the last line.
- Scrollable log (last 8 lines) showing clone output.
- `esc` cancels remaining clones; for new tasks the workspace dir
  is cleaned up, for add-repos already-cloned repos are kept.
- On completion: "Done!" then any key to dismiss.

**Completion/destruction progress** shows:
- "Stopping Claude" with spinner (waiting for graceful SIGINT shutdown,
  up to 5 seconds).
- A forget checklist with one line per *working copy*, not per repo
  (multi_repo only), so a task holding three checkouts of one repo shows
  three lines under three identities.
- Workspace directory removal.
- No cancel — destruction runs to completion.

## Task Lifecycle Integration

| Action   | Workspace behavior | `workspace_repos` rows |
|----------|-------------------|------------------------|
| Create   | Create workspace or pick cwd | One row per working copy, initial ones included |
| Park     | No change | No change |
| Unpark   | No change | No change |
| Freeze   | No change (preserve uncommitted work) | No change |
| Wake     | No change (workspace dir still exists) | No change |
| Complete | Destroy workspace (forget + rm -rf) | Deleted, or reassigned if the dir is shared |
| Failed   | No change — a diagnosis, not a teardown | No change |
| Relaunch | No change | No change |

**Freeze and park are no-ops here.** They move or close a tmux window.
The working copies, their VCS identities, and their provenance rows are
all exactly where the task left them, which is the whole point of
freezing: uncommitted work survives.

**`failed` is not a teardown.** Only the reconciler sets it, and only as
a diagnosis of a window that vanished. It deliberately leaves the
directories and the rows alone so that a later `Complete` on that same
task still knows every identity it has to forget. `Manager.Complete` is
the only path to a terminal state a human drives, and therefore the only
place identities are released.

### Workspace Destruction

1. Claude is stopped via SIGINT with a graceful shutdown timeout (falls
   back to tmux kill-window).
2. `DestroyRepoList` builds **one entry per working copy**, not per repo:
   the recorded `workspace_repos` rows first in row order, then any
   repo-looking directory no row covers. For each entry, `jj workspace
   forget <recorded vcs_name>` or `git worktree remove` +
   `git branch -d <recorded branch>`, run from the source repo.
3. `rm -rf` the workspace directory (unconditional, removes
   everything including non-repo files).
4. Errors are logged but don't block the state transition.

**Per working copy, via the recorded rows.** A task holding three
checkouts of one repo owns three jj workspace names, and only the initial
one is named after the task. A loop over *repos* would forget the first
identity three times and the other two never, leaking a `jj workspace`
per slot into the source repo. Cleanup therefore forgets the identity the
row *records* rather than one derived from the directory name — a
derivation cannot name a slot.

**A row with no directory still counts.** A recorded row whose directory
is already gone stays in the destroy list, because the identity it names
is still claimed in the source repo.

**A directory with no row falls back.** One-off clones made by hand, and
workspaces predating the table that the backfill skipped, get the old
directory-name derivation as a best effort. The filesystem scan behind
the rows only runs where the workspace directory is a *container* — in
`single_repo` it is the checkout itself, so cleanup goes through
`ForgetSingleRepoWorkspace`, which asks every repo in turn.

**Shared directories reassign their rows.** When another task still
shares the workspace directory, nothing is destroyed and nothing is
forgotten — but the rows are not deleted either. `releaseWorkspaceRepos`
hands them to the oldest surviving sharer, which is the task that will
eventually tear the directory down and needs every VCS identity to do it.
Deleting them would strand the identities behind a derivation that cannot
name a slot. (Deleting is the right move when the directory *is*
destroyed: it releases the task's name and its identities from the
`(repo_name, vcs_name)` unique constraint so the name can be reused.)

This is also what makes completing a *fork* safe. Forking is not
slot-aware, but a fork that shares its owner's workspace destroys nothing
and forgets nothing, so the owner's slots — which the fork never had a
claim on — survive it.

**Branch safety:** `git branch -d` (not `-D`) refuses to delete
branches with unpushed commits. If a branch has unpushed work,
it's kept as a safety net and the completion confirmation modal
warns about it, naming the branch that will actually survive
(`workspace.GitBranchFor`, so a slot's own branch rather than
`krang/<task>`). Surviving branches are findable via
`git branch | grep krang/`.

## Sandbox Integration

Workspace tasks launch Claude in a subdirectory of the metarepo.
Sandboxes (like safehouse) block reads above the workdir by default,
which breaks two things:

1. **Config file walking** — Claude walks upward looking for
   `.mcp.json`, `CLAUDE.md`, `.claude/` etc. Grant read access to
   these paths in the metarepo root via `{{.KrangDir}}`.

2. **VCS back-references** — both jj workspaces and git worktrees
   are lightweight: they store a pointer back to the source repo's
   object store (`.jj/repo` or `.git/worktrees/`). Without access
   to the source repos directory, all VCS operations fail with
   "Operation not permitted". Grant read+write access to
   `{{.ReposDir}}` so tasks can read history and create commits.

Sandbox profiles of type `command` support Go template variables:

| Variable | Value |
|----------|-------|
| `{{.KrangDir}}` | Krang's working directory (metarepo root) |
| `{{.TaskCwd}}` | Task's original launch cwd (stable, not live) |
| `{{.TaskName}}` | Task name |
| `{{.ReposDir}}` | Absolute path to repos directory (empty if no krang.yaml) |

Example granting config reads and full VCS access:

```yaml
sandboxes:
  default:
    type: command
    command: safehouse --add-dirs-ro={{.KrangDir}}/.mcp.json:{{.KrangDir}}/CLAUDE.md:{{.KrangDir}}/.claude --add-dirs={{.ReposDir}} --env-pass KRANG_STATEFILE --env-pass KRANG_DEBUG
default_sandbox: default
```

Since both jj and git now use lightweight linked workspaces,
`--add-dirs={{.ReposDir}}` is needed for all VCS types.

Falls back to the raw string on template parse errors.

## DB Schema

Migration V5 adds `workspace_dir TEXT NOT NULL DEFAULT ''` to the
tasks table. Empty string = no workspace (backward compatible).

Migration V8 adds `workspace_repos`, which records where each working
copy in a task's workspace directory came from and which VCS identity it
owns:

```sql
CREATE TABLE IF NOT EXISTS workspace_repos (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id       TEXT NOT NULL REFERENCES tasks(id),
	repo_name     TEXT NOT NULL,
	dir_name      TEXT NOT NULL,
	vcs           TEXT NOT NULL CHECK(vcs IN ('jj', 'git')),
	vcs_name      TEXT NOT NULL,
	slot_label    TEXT NOT NULL DEFAULT '',
	base_revision TEXT NOT NULL DEFAULT '',
	created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	UNIQUE(task_id, dir_name),
	UNIQUE(repo_name, vcs_name)
);

CREATE INDEX IF NOT EXISTS idx_workspace_repos_task ON workspace_repos(task_id);
```

| Column | Holds |
|---|---|
| `repo_name` | The repo under `repos_dir` this working copy was made from |
| `dir_name` | `SlotIdentity.DirName()` — the directory inside the workspace dir |
| `vcs` | `jj` or `git`, as detected on the source repo at creation |
| `vcs_name` | `VCSName()` for jj, `GitBranch()` for git — what cleanup forgets |
| `slot_label` | `SlotIdentity.Label`; empty for a task's initial working copy |
| `base_revision` | The effective base the working copy started from; empty on rows written before it was recorded, and on backfilled rows |

**`UNIQUE(task_id, dir_name)`** is one working copy per directory.
**`UNIQUE(repo_name, vcs_name)`** is the jj/git invariant: identities are
unique per source repo, across all tasks. Completing a task releases its
rows so a reused task name doesn't collide with it.

**Every working copy gets a row, including initial ones.** The reconcile
backfill (`Manager.backfillWorkspaceRepos`) only fires for tasks with
*zero* rows, so a partially recorded task would never have the rest
filled in. `Manager.CreateSlot` creates and records in one step; the
TUI's task-creation flow builds the workspace before the task row exists,
so provenance rides on the progress entries and is written via
`Manager.RecordSlot` once the task is created.

The events table cannot serve this purpose — it is trimmed on every
reconcile.

## Reading a Workspace Back

| Function | Answers |
|---|---|
| `PresentDirs` | The raw directory scan — names only |
| `PresentSlots` | Each directory resolved to the repo it holds, preferring recorded rows and falling back to `ParseSlotDirName` |
| `PresentRepos` | The *distinct* repos, which is what the repo picker hides |

`PresentRepos` is the one the "already present" filter uses: a second
slot of a repo must not read as an unknown repo of its own.

## Packages

| Package | Key types/functions |
|---------|-------------------|
| `internal/workspace/reposets.go` | `RepoSets`, `Load()`, `ListRepos()`, `DetectVCS()`, `ResolveRepos()` |
| `internal/workspace/slot.go` | `SlotIdentity`, `DirName()`, `VCSName()`, `GitBranch()`, `ValidateSlotLabel()`, `ResolveSlotIdentity()`, `SuggestSlotLabel()`, `ParseSlotDirName()`, `PresentSlots()`, `PresentRepos()` |
| `internal/workspace/workspace.go` | `Create()`, `AddRepos()`, `Destroy()`, `PresentDirs()`, `CreateWorkspaceDir()`, `CloneRepoAs()`, `RepoProvenance`, `ForgetRepo()`, `DestroyRepoList()`, `GitBranchFor()`, `ForgetSingleRepoWorkspace()` |
| `internal/db/workspacerepos.go` | `WorkspaceRepoStore` — insert, list by task, reassign, delete |
| `internal/task/slots.go` | `Manager.CreateSlot()`, `Manager.RecordSlot()` |
| `internal/tui/repopicker.go` | `repoPicker` — custom toggle-list component |
| `internal/tui/forms.go` | `newWorkspaceTaskForm()` |

The HTTP API and CLI that sit on top of this
(`GET /api/workspace`, `POST /api/workspace/add`,
`DELETE /api/workspace/slot`, and `krang workspace`) are documented in
[architecture.md](../architecture.md#workspace-api).

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
- **Slot name already taken in the source repo:** Refuse before writing
  anything. Never overwrite an existing jj workspace or git branch.
- **Slot directory would spell a managed repo's name:** Rejected
  outright — `ParseSlotDirName` could no longer tell the two apart.
- **Directory with no provenance row:** Cleanup falls back to the
  directory-name derivation; the API lists it with `recorded: false`.
- **Provenance row with no directory:** Still listed (`exists: false`)
  and still forgotten on cleanup — the identity is real either way.
- **Independent fork of a workspace holding labeled slots:** A gap, not
  a feature. `ForkRepo` treats each directory name as a repo name, so
  `api-server--tests` resolves to a repo that doesn't exist and the fork
  fails at creation rather than producing something cleanup would
  mishandle.
