package db

const schemaV1 = `
CREATE TABLE IF NOT EXISTS tasks (
	id           TEXT PRIMARY KEY,
	name         TEXT NOT NULL UNIQUE,
	prompt       TEXT,
	state        TEXT NOT NULL DEFAULT 'active'
	             CHECK(state IN ('active', 'parked', 'dormant', 'completed', 'failed')),
	attention    TEXT NOT NULL DEFAULT 'ok'
	             CHECK(attention IN ('ok', 'waiting', 'permission', 'error', 'done')),
	session_id   TEXT,
	cwd          TEXT NOT NULL,
	tmux_window  TEXT,
	summary      TEXT NOT NULL DEFAULT '',
	summary_hash TEXT NOT NULL DEFAULT '',
	created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_tasks_state ON tasks(state);

CREATE TABLE IF NOT EXISTS events (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id    TEXT NOT NULL REFERENCES tasks(id),
	event_type TEXT NOT NULL,
	payload    TEXT,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_events_task ON events(task_id, created_at);
`
