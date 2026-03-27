# Task Forking

Fork an existing task to create a new task with the same conversation
history but an independent workspace. The new Claude session starts
with full context of what was discussed and built, but diverges from
that point forward. Useful for "let me try a different approach"
without losing the current one.

## Claude Side

Claude Code supports `--resume <session-id> --fork-session`, which
creates a new session seeded with the original's history. Krang would
create a new task, spawn Claude with this flag combo, and adopt the
new session via the normal `SessionStart` hook flow.

## Workspace Side

The interesting complexity is workspace handling. A forked task needs
its own workspace so the two tasks don't step on each other. The
approach depends on the VCS.

### Jujutsu (straightforward)

jj automatically commits all working-copy changes, so at fork time
the source workspace's state is already captured in a commit. The fork
flow:

1. Read the current jj commit from the source workspace
   (`jj log --limit 1` or similar)
2. Create a new workspace for the forked task via `jj workspace add`
3. Edit the new workspace to the same commit (`jj edit <commit>` from
   the new workspace)
4. The new workspace now has identical code, fully independent. Both
   tasks can diverge freely.

This works for both single-repo and multi-repo strategies — repeat for
each repo in the workspace.

### Git (hard)

Git doesn't auto-commit, so uncommitted changes are the problem.
Options, none great:

- **`git worktree add`** only works from a commit, so uncommitted
  changes are lost.
- **Stash dance:** `git stash` → `git worktree add` → apply stash in
  the new worktree. Fragile — stash apply in a different worktree is
  not straightforward and can conflict.
- **Filesystem copy:** Copy the entire repo directory. Works but it's
  not a proper worktree (no shared object store, cleanup is messier,
  pushes from the copy may surprise the user).
- **Temporary commit:** Force a temp commit, worktree from that, then
  soft-reset in the original. Fragile and surprising to the user.

For now, git workspace forking could be limited to repos with a clean
working tree, or documented as a jj-only feature.

## UX

- Keybinding in the detail modal (e.g. `d` for duplicate/diverge, or
  exposed via the command palette)
- Task name auto-generated as `<original>-fork` or `<original>-2`
  (user can rename)
- The original task's flags and companion state do not carry over —
  it's a fresh task with old conversation context

## Open Questions

- Should the forked task's name reference the original? Helpful for
  tracking lineage but could get long with multiple forks.
- Should there be a way to fork without a workspace (just fork the
  conversation, user picks a new cwd)? Simpler and covers the
  no-workspace case.
- Multi-repo workspaces: fork all repos or let the user pick a
  subset? Probably all, to match the conversation context.
