CREATE TABLE scenario_runs (
	id TEXT PRIMARY KEY,
	definition_name TEXT NOT NULL,
	organization_template TEXT NOT NULL,
	random_seed INTEGER NOT NULL DEFAULT 0 CHECK (random_seed >= 0),
	status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
	clock_start TEXT NOT NULL,
	current_event_id TEXT NOT NULL DEFAULT '',
	events_total INTEGER NOT NULL CHECK (events_total >= 0),
	events_succeeded INTEGER NOT NULL DEFAULT 0 CHECK (events_succeeded >= 0),
	resources_created INTEGER NOT NULL DEFAULT 0 CHECK (resources_created >= 0),
	usage_events_created INTEGER NOT NULL DEFAULT 0 CHECK (usage_events_created >= 0),
	metering_records_created INTEGER NOT NULL DEFAULT 0 CHECK (metering_records_created >= 0),
	bill_line_items_created INTEGER NOT NULL DEFAULT 0 CHECK (bill_line_items_created >= 0),
	bills_issued INTEGER NOT NULL DEFAULT 0 CHECK (bills_issued >= 0),
	error_message TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	completed_at TEXT,
	CHECK (trim(id) <> ''),
	CHECK (trim(definition_name) <> ''),
	CHECK (trim(organization_template) <> ''),
	CHECK (trim(clock_start) <> ''),
	CHECK (completed_at IS NULL OR completed_at >= started_at)
);

CREATE INDEX idx_scenario_runs_status_started
ON scenario_runs (status, started_at);

CREATE TABLE scenario_run_events (
	id TEXT PRIMARY KEY,
	scenario_run_id TEXT NOT NULL REFERENCES scenario_runs(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	scenario_event_id TEXT NOT NULL,
	scenario_event_sequence INTEGER NOT NULL CHECK (scenario_event_sequence > 0),
	action TEXT NOT NULL,
	scheduled_at TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('succeeded', 'failed')),
	resource_id TEXT NOT NULL DEFAULT '',
	usage_event_id TEXT NOT NULL DEFAULT '',
	generated_usage_event_count INTEGER NOT NULL DEFAULT 0 CHECK (generated_usage_event_count >= 0),
	metering_records_created INTEGER NOT NULL DEFAULT 0 CHECK (metering_records_created >= 0),
	bill_line_items_created INTEGER NOT NULL DEFAULT 0 CHECK (bill_line_items_created >= 0),
	bill_id TEXT NOT NULL DEFAULT '',
	error_message TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	completed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(scenario_run_id) <> ''),
	CHECK (trim(scenario_event_id) <> ''),
	CHECK (trim(action) <> ''),
	CHECK (trim(scheduled_at) <> ''),
	UNIQUE (scenario_run_id, scenario_event_id)
);

CREATE INDEX idx_scenario_run_events_run_sequence
ON scenario_run_events (scenario_run_id, scenario_event_sequence);

PRAGMA user_version = 15;
