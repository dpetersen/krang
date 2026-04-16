# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

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
