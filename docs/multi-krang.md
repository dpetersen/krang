# Multi-Krang Support

## Context

Krang currently hardcodes a single HTTP hook endpoint (`127.0.0.1:19283`),
a single parked session name (`krang-parked`), and defaults to a single
shared DB (`~/.config/krang/krang.db`). This means only one krang instance
can run at a time. Multi-krang support is a prerequisite for the workspace
feature, where different metarepos (e.g. `~/code/launchdarkly`,
`~/dev/krang`) each run their own krang.

## Design

### 1. Dynamic Port with State File

Replace the hardcoded port with dynamic allocation. Krang writes its
state to the instance directory at
`~/.config/krang/instances/<encoded-cwd>/krang-state.json` (alongside
the instance-scoped DB) so the relay script can always find the
current port. No project-level files to gitignore.

**State file format** (`krang-state.json`):
```json
{
  "port": 52341
}
```

**`internal/hooks/server.go`:**
- Remove `const ListenAddr = "127.0.0.1:19283"`
- `Start()` binds to `127.0.0.1:0` to get a free port from the OS
- Add `Port() int` method that returns the bound port
- On startup, check if an existing state file's port has a responding
  `/health` endpoint — if so, fail with
  `"krang is already running for this directory on port <port>"`
- After binding, write the port to the state file
- On shutdown, remove the state file

**`cmd/root.go`:**
- After `hookServer.Start()`, read `hookServer.Port()` and pass it down
  to the manager and TUI so it can be displayed and propagated

### 2. Command Hook via Relay Script

Replace `type: "http"` hooks in Claude's settings.json with
`type: "command"` hooks pointing at a static relay script.

**`~/.config/krang/hooks/relay.sh`:**
```bash
#!/bin/bash
[ -z "$KRANG_STATEFILE" ] && exit 0
[ ! -f "$KRANG_STATEFILE" ] && exit 0
PORT=$(jq -r .port "$KRANG_STATEFILE" 2>/dev/null)
[ -z "$PORT" ] && exit 0
cat | curl -s -X POST -H 'Content-Type: application/json' \
  -d @- "http://127.0.0.1:$PORT/hooks/event" >/dev/null 2>&1
exit 0
```

**`internal/hooks/install.go`:**
- `Install()` writes the relay script to `~/.config/krang/hooks/relay.sh`
  (chmod 755), idempotently
- Changes hook entries from `type: "http", url: "..."` to
  `type: "command", command: "<path>/relay.sh"`
- `Uninstall()` removes command-type krang hooks (match on the relay
  script path instead of the old URL)
- Add migration: `Install()` should also remove any old HTTP-type krang
  hooks (matching the old `hookURL`) so upgrading is clean

### 3. Environment Propagation (KRANG_STATEFILE)

When krang spawns tmux windows, set `KRANG_STATEFILE` so the relay
script knows where to read the port. Uses inline `export` in the shell
command string (not `tmux set-environment`) to avoid leaking into
non-krang windows in the same session.

**`internal/task/manager.go`:**
- `Manager` stores the state file path (passed in from `cmd/root.go`)
- `buildClaudeCommand` prepends
  `export KRANG_STATEFILE=<path>;` to the command string
- Affects all call sites: `CreateTask`, `Wake`, `Relaunch`

### 4. Instance-Scoped Tmux Sessions

Derive session names from the metarepo directory so multiple krangs
don't collide.

**`internal/tmux/session.go`:**
- Replace `const ParkedSession = "krang-parked"` with a function
  `ParkedSessionName(instanceID string) string` returning
  `"krang-" + instanceID + "-parked"`
- Instance ID = `<basename>-<short-hash>` where basename is the
  directory name and short hash is the first 4 hex chars of the
  SHA-256 of the full absolute path (e.g. `launchdarkly-a3f2`)

**`cmd/root.go`:**
- Compute instance ID from `os.Getwd()`
- Pass it through to the manager and tmux functions
- The krang TUI window itself gets renamed to `krang` (already happens)
  — this is fine since it's scoped to the user's active session

**Reconciliation** already scopes to `m.activeSession` and
`ParkedSession`, so changing those names is sufficient.

### 5. Instance-Scoped Database

Use Claude-style path encoding for deterministic per-directory DBs.

**`internal/db/db.go`:**
- When `KRANG_DB` is unset, derive the default path from cwd:
  `~/.config/krang/instances/<encoded-cwd>/krang.db`
- The `encodePath` function already exists in `internal/task/manager.go`
  — extract it to a shared `internal/pathutil` package (or just
  `internal/db` itself)
- Keep `KRANG_DB` override working for dev (`mise.toml` sets it)

### 6. Window Prefixes — No Change

`K!` and `KF!` prefixes stay global. Reconciliation only looks at
windows in its own sessions, so there's no collision risk even if
another krang uses the same prefixes in different sessions.

## File Changes Summary

| File | Change |
|------|--------|
| `internal/hooks/server.go` | Dynamic port via `:0`, state file write/cleanup, `Port()` method |
| `internal/hooks/install.go` | Command hooks + relay script, migrate old HTTP hooks |
| `internal/tmux/session.go` | Parameterize parked session name |
| `internal/task/manager.go` | Store state file path, propagate via KRANG_STATEFILE |
| `internal/db/db.go` | Instance-scoped default DB path |
| `cmd/root.go` | Compute instance ID, wire state file + instance through |

## Edge Cases

- **Stale state file** — if krang crashes without cleaning up the state
  file, the next startup reads the port from it and hits `/health`. No
  response means it's stale — overwrite and proceed. A response means
  another krang is actually running.
- **Dev mode** — `KRANG_DB` in `mise.toml` overrides the DB path, so
  dev workflow is unchanged.

## Verification

1. Run `mise run run` — krang starts on a dynamic port, relay script
   is installed, command hooks appear in `~/.claude/settings.json`,
   state file is written to `~/.config/krang/instances/`
2. Create a task — confirm `KRANG_STATEFILE` is set in the tmux pane
   (`env | grep KRANG_STATEFILE`)
3. Confirm hook events flow: Claude session sends events → relay script
   reads state file → krang HTTP server → events appear in TUI
4. Stop krang — confirm state file is removed
5. Run a second krang instance from a different directory — confirm it
   gets its own port, DB, and parked session
6. Run tests: `mise run test`

## Implementation Order

1. Extract `encodePath` to shared location + add instance ID helper
2. Instance-scoped DB path
3. Dynamic port + state file in hook server
4. Relay script + command hook installation (+ old hook migration)
5. KRANG_STATEFILE propagation in tmux window creation
6. Instance-scoped tmux session names
7. Wire everything together in `cmd/root.go`
