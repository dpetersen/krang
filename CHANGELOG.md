# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed

- Integration tests no longer run against the default tmux server. Each
  test now gets a private server via `tmux -L`, so a test run can't
  disturb a live krang instance on the same machine. Previously the
  harness set `HOME`, `KRANG_CLAUDE_CMD`, and `FAKECLAUDE_CONTROLDIR` in
  the shared server's *global* environment, which every window opened
  afterwards inherited — a real krang task launched during a test run
  would start the fake Claude binary against the test's temp-dir `HOME`.
  Teardown also killed sessions by name and unset those globals outright.

### Added

- Unified slot creation. Every working copy krang makes for a task now
  goes through one path that gives it an explicit `SlotIdentity`
  (task, repo, label) and derives its directory name, jj workspace
  name, and git branch from it. A task's initial working copy keeps the
  names it has always had — directory `<repo>`, VCS identity `<task>` —
  so nothing on disk is renamed. Additional slots of the same repo get
  directory `<repo>--<label>`, jj workspace `<task>--<repo>--<label>`,
  and branch `krang/<task>--<repo>--<label>`, auto-numbering (2, 3, …)
  when no label is given. Creation records a `workspace_repos` row for
  every working copy, including initial ones.

- Slot creation refuses collisions instead of overwriting. The computed
  jj workspace name is checked against `jj workspace list` in the source
  repo and the git branch against `git branch --list` before anything is
  written, and a slot directory that would spell a managed repo's name
  is rejected outright.

- Workspace repo provenance. A new `workspace_repos` table records, for
  every working copy inside a task's workspace directory, which repo it
  was created from and which jj workspace / git branch it owns. Cleanup
  now forgets the recorded identity instead of inferring both from the
  directory name, which is a prerequisite for holding more than one
  working copy of the same repo in a task. Workspaces created before
  the table are backfilled on reconcile using the old derivation, and
  directories with no row keep the old behavior as a fallback.

- `KRANG_TMUX_SOCKET` pins krang to a specific tmux server. Unset (the
  default) targets the default server exactly as before; the integration
  harness sets it per-test.

- Frozen tasks whose Claude transcript no longer exists are marked with
  a ⚠ next to the name, with an explanation in the detail modal.
  Claude deletes transcripts older than `cleanupPeriodDays` (30 by
  default), which leaves a frozen task holding a session ID that
  resolves to nothing — krang previously showed it as healthy and let
  you discover the problem from Claude's "no such session" error.
  Unfreezing such a task now fails up front with a clear message.
- Help (`?`) documents the row markers next to task names (☠, ⚠, `+`),
  which were previously undocumented.
- Tasks that stopped with a `ScheduleWakeup` or `Monitor` armed show a ⏰
  next to their attention state. Both tools hand control back to the
  prompt while arranging for Claude to continue on its own, so the task
  read as plain "done"/"wait" when it was really waiting on a timer or
  an event stream. The marker expires on the tool's own deadline
  (`delaySeconds` / `timeout_ms`), since Claude Code emits no hook event
  when a wakeup fires or a monitor ends.

### Changed

- Reusing a task name no longer force-deletes the leftover
  `krang/<task>` branch. Cleanup goes out of its way to keep branches
  holding unpushed work, and creation then threw them away. Krang now
  reclaims the branch only when `git branch -d` agrees nothing is lost
  (fully merged, not checked out anywhere) and otherwise refuses with an
  error naming the branch.

- The repo picker's "already present" filter is slot-aware. It hides the
  repos a workspace holds rather than the directory names, so a second
  slot of a repo no longer reads as an unknown repo of its own.

- Select the newly-created task in the main list as soon as it appears,
  so the first lifecycle action after dismissing the creation (or fork)
  modal targets the task you just made instead of the previously-
  selected row. Applies to plain creation, workspace creation, and both
  fork modes.

## [1.0.0-beta.3] - 2026-04-16

### Fixed

- Fix forking multi-repo workspaces that contain non-repo directories or
  root-level files. Non-repo items are now copied to the fork and shown
  in the progress wizard as "(file)" or "(dir)".
- CWD picker ignoring filtered selection when pressing Enter.
- Fix false "krang is already running" error when only the parked session
  exists. Session checks now use exact tmux name matching and verify
  liveness via the hook server health endpoint, with specific error
  messages for live instances vs stale sessions.
- Clean up the parked tmux session on exit when no tasks are parked,
  preventing stale sessions from lingering.
- Fix unfreeze launching at the wrong cwd (often the user's home
  directory) when a stale session file existed in another project
  directory — typically left over from a fork whose workspace was
  deleted without the fork session ever being adopted. findSessionCwd
  now prefers the task's own cwd and, failing that, prefers matches
  whose decoded path still exists over ones pointing at deleted
  workspaces.
- Clean up copied source session files when a forked task is completed.
  Previously these were only removed on session adoption, so forks
  that were completed before Claude sent SessionStart (e.g. after a
  launch failure) left stale files behind that confused future
  resumes of the source task.

## [1.0.0-beta.2] - 2026-04-13

### Fixed

- Prevent idle_prompt notification from overwriting classified "done"
  state. Claude Code fires an idle_prompt ~60s after going idle, which
  was flipping tasks from green back to yellow after the classifier had
  already marked them done.

## [1.0.0-beta.1] - 2026-04-08

Initial beta release.

[Unreleased]: https://github.com/dpetersen/krang/compare/v1.0.0-beta.3...HEAD
[1.0.0-beta.3]: https://github.com/dpetersen/krang/compare/v1.0.0-beta.2...v1.0.0-beta.3
[1.0.0-beta.2]: https://github.com/dpetersen/krang/compare/v1.0.0-beta.1...v1.0.0-beta.2
[1.0.0-beta.1]: https://github.com/dpetersen/krang/releases/tag/v1.0.0-beta.1
