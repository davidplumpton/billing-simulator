CREATE TABLE workspace_metadata (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL,
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT INTO workspace_metadata (key, value)
VALUES ('schema_kind', 'aws-billing-simulator');

PRAGMA user_version = 1;
