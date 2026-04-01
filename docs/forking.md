# Task Forking

Fork an existing task to create a new task with the same conversation
history but divergent from that point forward. Useful for "let me try
a different approach" without losing the current one.

## How It Works

Press `d` in the detail modal on any task with a session ID (active,
parked, or dormant). A fork dialog lets you name the fork and choose
a workspace mode.

Claude Code's `--resume <session-id> --fork-session` creates a new
session seeded with the original's history. Krang creates the new
task, copies session files if needed, and launches Claude with this
flag. The forked session gets a new session ID via the normal
`SessionStart` hook flow.

## Workspace Modes

### Independent (default)

Creates a new workspace, fully separate from the original. Both tasks
can work concurrently without interference.

**jj repos:** `jj duplicate @` creates a sibling commit (same content,
same parent, no link), then `jj workspace add` + `jj edit` puts the
new workspace on the duplicate. Changes to either workspace don't
affect the other — no auto-rebase.

**git repos:** `cp -a` copies the entire directory (preserving all
committed, staged, unstaged, and untracked changes), then
`git checkout -b <fork-name>` creates a new branch so pushes don't
collide.

### Shared

Both tasks share the same workspace directory. Only the conversation
forks — no workspace duplication. A warning is shown in the fork
dialog about concurrent edit risk.

Useful for quick tasks where dealing with getting changes between
disconnected workspaces is painful, especially with git.

**Cleanup behavior:** When completing a task with a shared workspace,
krang checks if other active/parked/dormant tasks use the same
directory. If so, cleanup is skipped and a message is shown. The
workspace is only destroyed when the last task using it completes.

### Non-workspace tasks

Tasks without managed workspaces always fork in shared mode (same
cwd). A warning about concurrent edits is shown.

## Session File Handling

Claude resolves sessions relative to the project directory
(`~/.claude/projects/<encoded-cwd>/`). When forking into a new
workspace (different cwd), the session file won't exist under the new
path. Krang copies the session JSONL and companion directory to the
new project path before launching. After `SessionStart` confirms the
fork, the copies are cleaned up.

### Contested sessions

Between fork launch and `SessionStart`, the forked Claude sends
events using the source session ID. Without mitigation, these events
would corrupt the original task's state (particularly cwd). Krang
tracks "contested sessions" — source session IDs with an active fork
in flight — and routes events to the correct task based on cwd
matching. The resolved task ID is passed directly to the event
handler to avoid re-lookup races.

## Lineage Tracking

Forked tasks store `source_task_id` in the database. The detail modal
shows "forked from: <name>" and "workspace shared with: <names>" when
applicable. The completion confirmation modal also shows shared
workspace status.

## Why Not Linked Mode?

A "linked" mode (`jj workspace add -r @`, creating a child commit
that auto-rebases from the source) was considered and dropped. jj's
stale workspace handling loses working copy changes when both
workspaces are edited concurrently (jj-vcs/jj#1310). Since concurrent
editing is the primary fork use case, linked mode is unusable.
