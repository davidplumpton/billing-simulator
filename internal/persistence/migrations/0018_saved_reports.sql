CREATE TABLE saved_reports (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	owner_account_id TEXT NOT NULL,
	owner_role TEXT NOT NULL CHECK (owner_role IN ('management-account', 'member-account', 'finance', 'instructor')),
	date_range_start TEXT NOT NULL,
	date_range_end TEXT NOT NULL,
	granularity TEXT NOT NULL CHECK (granularity IN ('daily', 'monthly', 'hourly')),
	filters_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(filters_json) AND json_type(filters_json) = 'object'),
	groupings_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(groupings_json) AND json_type(groupings_json) = 'array' AND json_array_length(groupings_json) <= 2),
	metrics_json TEXT NOT NULL DEFAULT '["unblended_cost"]' CHECK (json_valid(metrics_json) AND json_type(metrics_json) = 'array' AND json_array_length(metrics_json) > 0),
	chart_type TEXT NOT NULL CHECK (chart_type IN ('table', 'line', 'bar', 'stacked_bar')),
	last_run_at TEXT,
	last_run_status TEXT NOT NULL DEFAULT 'never_run' CHECK (last_run_status IN ('never_run', 'succeeded', 'failed')),
	last_run_row_count INTEGER NOT NULL DEFAULT 0 CHECK (last_run_row_count >= 0),
	last_run_total_unblended_cost_micros INTEGER NOT NULL DEFAULT 0 CHECK (last_run_total_unblended_cost_micros >= 0),
	last_run_error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(name) <> ''),
	CHECK (trim(owner_account_id) <> ''),
	CHECK (date_range_start < date_range_end),
	CHECK (
		(last_run_status = 'never_run' AND last_run_at IS NULL)
		OR (last_run_status <> 'never_run' AND last_run_at IS NOT NULL)
	)
);

CREATE UNIQUE INDEX idx_saved_reports_owner_name
ON saved_reports (owner_account_id, lower(name));

CREATE INDEX idx_saved_reports_owner_updated
ON saved_reports (owner_account_id, updated_at DESC, id DESC);

CREATE INDEX idx_saved_reports_last_run
ON saved_reports (last_run_status, last_run_at DESC);

PRAGMA user_version = 18;
