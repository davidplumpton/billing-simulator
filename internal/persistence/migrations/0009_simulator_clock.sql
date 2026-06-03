CREATE TABLE simulator_clock (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	current_time_utc TEXT NOT NULL CHECK (trim(current_time_utc) <> ''),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT INTO simulator_clock (id, current_time_utc)
VALUES (1, '2026-02-01T00:00:00Z');

PRAGMA user_version = 9;
