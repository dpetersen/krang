# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

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
## [1.0.0-beta.2] - 2026-04-13

### Fixed

- Prevent idle_prompt notification from overwriting classified "done"
  state. Claude Code fires an idle_prompt ~60s after going idle, which
  was flipping tasks from green back to yellow after the classifier had
  already marked them done.

## [1.0.0-beta.1] - 2026-04-08

Initial beta release.

[Unreleased]: https://github.com/dpetersen/krang/compare/v1.0.0-beta.2...HEAD
[1.0.0-beta.2]: https://github.com/dpetersen/krang/compare/v1.0.0-beta.1...v1.0.0-beta.2
[1.0.0-beta.1]: https://github.com/dpetersen/krang/releases/tag/v1.0.0-beta.1
