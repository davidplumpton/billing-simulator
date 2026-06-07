CREATE TABLE workspace_export_files (
	id TEXT PRIMARY KEY,
	filename TEXT NOT NULL UNIQUE,
	export_type TEXT NOT NULL,
	billing_period_start TEXT NOT NULL DEFAULT '',
	billing_period_end TEXT NOT NULL DEFAULT '',
	payer_account_id TEXT NOT NULL DEFAULT '',
	usage_account_id TEXT NOT NULL DEFAULT '',
	size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
	checksum_sha256 TEXT NOT NULL,
	generation_parameters_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(filename) <> ''),
	CHECK (trim(export_type) <> ''),
	CHECK ((billing_period_start = '' AND billing_period_end = '') OR (billing_period_start <> '' AND billing_period_end <> '')),
	CHECK (length(checksum_sha256) = 64),
	CHECK (json_valid(generation_parameters_json) AND json_type(generation_parameters_json) = 'object')
);

CREATE INDEX idx_workspace_export_files_type_period
ON workspace_export_files (export_type, billing_period_start, billing_period_end, created_at);

CREATE INDEX idx_workspace_export_files_payer_period
ON workspace_export_files (payer_account_id, billing_period_start, billing_period_end, created_at);

PRAGMA user_version = 32;
