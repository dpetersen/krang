# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Detail modal lists a task's working copies, grouped under the repo each one
  is a checkout of. The initial checkout reads quietly; every added slot is
  called out with its label and the base it was created from. Directories with
  no provenance row are marked `unrecorded`, and recorded rows with nothing on
  disk are marked `missing`.
- Workspace mutations requested over the HTTP API now show a status line under
  the task table while they run (`agent add task=… alpha--tests`), including a
  count of anything queued behind the human's own workspace flow. The debug log
  gained a matching `started` line to go with the existing completion line.
- `krang workspace` — a CLI for the workspace API, so an agent inside a
  task window can change its own workspace without a human at the TUI.
  Four subcommands (`list`, `repos`, `add`, `remove`) sit next to `setup`
  and `teardown`, outside tmux; they find the running instance through
  `KRANG_STATEFILE` and call the loopback endpoints, which means the TUI
  process still does all the work and is still the single writer.

  The design target is a caller with no eyes. `--cwd` defaults to the
  process's working directory, so `krang workspace list` answers with no
  arguments at all from inside a workspace; `--task` overrides it.
  `--json` prints krang's response envelope byte for byte on every
  subcommand, and `add` prints the new working copy's absolute path and
  nothing else. Exit codes distinguish the four decisions a caller
  actually has to make: 0 success, 1 an error retrying can't fix, 2 a
  refusal that the *identical* command fixes once you've dealt with what
  the message names (`unsaved_work`, `label_required`, `slot_limit`, …),
  3 krang may or may not have applied it — do not blindly retry, and 4
  krang never took the request so retrying is safe. Every subcommand's
  `--help` names its endpoint, its defaults, and the whole exit-code
  table, because an agent reads one `--help`, not the tree.

- Workspace HTTP API. Four endpoints let a Claude session (or a future
  CLI subcommand) inspect and change a task's workspace without the user
  switching to the TUI:

  - `GET /api/workspace` lists every working copy the task holds, as
    `slots[]` of `{dir, repo, canonical_repo_path, vcs, vcs_name, slot,
    base, exists, recorded}`. Recorded `workspace_repos` rows come
    first; repo-looking directories krang never recorded follow with
    `recorded: false`, and a recorded row whose directory has been
    deleted is still listed with `exists: false`.
  - `GET /api/workspace/repos` lists the repos the metarepo makes
    available, as `{name, in_task, sets}`.
  - `POST /api/workspace/add` is one verb for both "a repo this task
    doesn't have" and "another checkout of one it does". A repo already
    in the task requires an explicit `label`, and the refusal suggests a
    free one. `base` selects the revset or commit-ish the slot starts
    from, defaulting to remote-default-branch detection. Slots are
    always created from the canonical repo, never from a sibling
    working copy.
  - `DELETE /api/workspace/slot` forgets the recorded VCS identity,
    removes the directory, and drops the row. It refuses with a 409 when
    `HasUncommittedChanges`/`HasUnpushedCommits` say work would be lost,
    naming what in a machine-readable `blockers[]`; `{"force": true}`
    proceeds. Removing a repo's last slot is how a repo leaves a task,
    and goes through the same gates.

  Callers name the task by `task` or by `cwd` (matched against live
  tasks' workspace directories), through one shared resolver used by all
  four. Mutations go through the existing serialization queue; the two
  reads skip it, because a listing takes no locks and should not wait
  behind a modal the human may leave open. A per-task cap of four
  working copies bounds sprawl on the API path, with the refusal naming
  what could be removed to make room. Adding to a workspace shared by
  several tasks is refused: nothing in the data model says which task
  owns a slot.

- Workspace requests are serialized through the TUI process. The hook
  HTTP server can now hand a typed `WorkspaceRequest` to the Bubble Tea
  model over a channel the model consumes the way it consumes hook
  events. The Update loop never blocks: it queues the request, runs it
  as a `tea.Cmd`, and replies on completion. Exactly one workspace
  mutation is in flight at a time — requests arriving while another
  runs, or while the human has a workspace modal open, queue FIFO, and
  the keyboard flows are refused while an agent request holds the slot.
  Every completed request writes an events-table row and a debug-log
  line. HTTP callers get a bounded wait and a machine-readable JSON
  failure (`{"status":"error","reason":…,"applied":…}`); a timeout is
  503 with `applied: "unknown"`, because abandoning the wait does not
  cancel the work.

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

- Bare `krang` refuses to launch inside a krang task window. A task window
  has `KRANG_STATEFILE` set, and starting the TUI there renamed the tmux
  session to the new instance's name — after which the instance that owns
  the window was hunting for its windows in a session that no longer
  answered to the name it knew, and neither one worked again without manual
  tmux surgery. The refusal is unconditional and there is no override flag:
  a krang inside a krang has no use case, and a flag for it would only ever
  be found by accident. The message points a deliberate caller at
  `env -u KRANG_STATEFILE krang`, and everyone else at the `krang workspace`
  subcommands, which talk to the running instance rather than starting a
  second one.

- The completion confirmation states how many working copies are about to be
  deleted — slots included — and names the *slot* holding uncommitted or
  unpushed work rather than the task. Each unpushed warning now reports the
  branch that will actually survive in the source repo
  (`krang/<task>--<repo>--<label>` for a slot), where before it reported
  `krang/<task>` for every entry, which was only ever right for a task's
  initial checkout.

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

### Fixed

- Nothing mutates before krang knows it can start. Startup validated in the
  wrong order: it renamed the caller's tmux session, created the parked
  session, ran first-time config setup, opened the database, and bound the
  hook server's port — and only then found out whether it had a terminal to
  draw on, because the terminal was never checked at all. Bubble Tea's
  `Run()` failing with "could not open a new TTY" *was* the check, and it
  ran last. Every launch precondition, the new nested-launch guard
  included, is now answered ahead of the first rename; the terminal one
  asks exactly what Bubble Tea asks (stdin, then `/dev/tty`).

- The integration harness no longer leaks the developer's own
  `KRANG_STATEFILE` into the test tmux server. The server inherits the
  environment of the client that starts it, so running the suite from
  inside a krang task window handed every test's krang — and every window
  it opened — a statefile pointing at the live instance. The launch now
  goes through `env -u KRANG_STATEFILE`, and the variable is unset in the
  test server's session and global environments alongside the `HOME` and
  `KRANG_CLAUDE_CMD` pins.

- Completing a task whose workspace directory another task still shares no
  longer deletes the provenance rows for the working copies in it. The
  directory survives the completion, so its rows now move to the surviving
  task — the one that will eventually tear it down and needs to know every
  VCS identity to forget. Previously the rows were dropped and the last task
  out fell back to deriving identities from directory names, which cannot
  name a slot, leaking a `jj workspace` per slot into the source repo.

- `workspace_root` refusals from `DELETE /api/workspace/slot` now answer 409
  instead of falling through to 500. It is a deliberate refusal like
  `unsaved_work` and `shared_workspace`, and completing the task resolves it;
  a 500 said krang had broken. The CLI branches on `reason`, so its exit
  codes are unchanged.

- `DestroyRepoList` no longer scans a `single_repo` workspace's
  subdirectories for working copies. There the workspace directory *is* the
  checkout, so its subdirectories are that repo's own contents; a vendored
  checkout inside one could be mistaken for a slot and aim cleanup at a repo
  nobody asked it to touch.

- `workspace_repos.base_revision` is now actually written. The column
  existed but every row got an empty string, so "where did this working
  copy start?" was unanswerable. The base is resolved before the working
  copy is created — the caller's `--base` when given, otherwise the
  detected remote default branch — and the same value that was handed to
  `jj workspace add -r` / `git worktree add` is what gets recorded.
  Recording the bookmark name afterwards would have been worse than
  nothing, since it points wherever the bookmark has since moved. Rows
  written before this change keep their empty value.

- Integration tests no longer run against the default tmux server. Each
  test now gets a private server via `tmux -L`, so a test run can't
  disturb a live krang instance on the same machine. Previously the
  harness set `HOME`, `KRANG_CLAUDE_CMD`, and `FAKECLAUDE_CONTROLDIR` in
  the shared server's *global* environment, which every window opened
  afterwards inherited — a real krang task launched during a test run
  would start the fake Claude binary against the test's temp-dir `HOME`.
  Teardown also killed sessions by name and unset those globals outright.

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
