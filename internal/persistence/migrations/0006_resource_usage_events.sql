CREATE TABLE resources (
	id TEXT PRIMARY KEY,
	account_id TEXT NOT NULL,
	region_code TEXT NOT NULL,
	service_code TEXT NOT NULL,
	resource_type TEXT NOT NULL,
	resource_name TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL CHECK (status IN ('planned', 'active', 'stopped', 'deleted')),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	started_at TEXT,
	stopped_at TEXT,
	deleted_at TEXT,
	attributes_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(attributes_json) AND json_type(attributes_json) = 'object'),
	event_source TEXT NOT NULL DEFAULT 'learner' CHECK (event_source IN ('learner', 'scenario', 'generator', 'system')),
	scenario_run_id TEXT,
	scenario_event_id TEXT,
	scenario_event_sequence INTEGER CHECK (scenario_event_sequence IS NULL OR scenario_event_sequence >= 0),
	notes TEXT NOT NULL DEFAULT '',
	CHECK (trim(id) <> ''),
	CHECK (trim(account_id) <> ''),
	CHECK (trim(region_code) <> ''),
	CHECK (trim(service_code) <> ''),
	CHECK (trim(resource_type) <> ''),
	CHECK (started_at IS NULL OR stopped_at IS NULL OR started_at <= stopped_at),
	CHECK (stopped_at IS NULL OR deleted_at IS NULL OR stopped_at <= deleted_at),
	CHECK (
		event_source <> 'scenario'
		OR (
			scenario_run_id IS NOT NULL
			AND trim(scenario_run_id) <> ''
			AND scenario_event_id IS NOT NULL
			AND trim(scenario_event_id) <> ''
		)
	)
);

CREATE INDEX idx_resources_account_service
ON resources (account_id, service_code, region_code);

CREATE INDEX idx_resources_status
ON resources (status);

CREATE INDEX idx_resources_scenario_provenance
ON resources (scenario_run_id, scenario_event_id);

CREATE TABLE resource_tags (
	id TEXT PRIMARY KEY,
	resource_id TEXT NOT NULL REFERENCES resources(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	tag_key TEXT NOT NULL,
	tag_value TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	removed_at TEXT,
	event_source TEXT NOT NULL DEFAULT 'learner' CHECK (event_source IN ('learner', 'scenario', 'generator', 'system')),
	scenario_run_id TEXT,
	scenario_event_id TEXT,
	scenario_event_sequence INTEGER CHECK (scenario_event_sequence IS NULL OR scenario_event_sequence >= 0),
	CHECK (trim(id) <> ''),
	CHECK (trim(resource_id) <> ''),
	CHECK (trim(tag_key) <> ''),
	CHECK (removed_at IS NULL OR removed_at > applied_at),
	CHECK (
		event_source <> 'scenario'
		OR (
			scenario_run_id IS NOT NULL
			AND trim(scenario_run_id) <> ''
			AND scenario_event_id IS NOT NULL
			AND trim(scenario_event_id) <> ''
		)
	)
);

CREATE INDEX idx_resource_tags_resource
ON resource_tags (resource_id);

CREATE INDEX idx_resource_tags_key_value
ON resource_tags (tag_key, tag_value);

CREATE INDEX idx_resource_tags_scenario_provenance
ON resource_tags (scenario_run_id, scenario_event_id);

CREATE UNIQUE INDEX idx_resource_tags_one_active_value
ON resource_tags (resource_id, tag_key)
WHERE removed_at IS NULL;

CREATE TABLE usage_events (
	id TEXT PRIMARY KEY,
	resource_id TEXT NOT NULL REFERENCES resources(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	account_id TEXT NOT NULL,
	service_code TEXT NOT NULL,
	usage_type TEXT NOT NULL,
	operation TEXT NOT NULL,
	region_code TEXT NOT NULL,
	usage_start_time TEXT NOT NULL,
	usage_end_time TEXT NOT NULL,
	usage_quantity_micros INTEGER NOT NULL CHECK (usage_quantity_micros > 0),
	usage_unit TEXT NOT NULL,
	attributes_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(attributes_json) AND json_type(attributes_json) = 'object'),
	tag_snapshot_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(tag_snapshot_json) AND json_type(tag_snapshot_json) = 'object'),
	event_source TEXT NOT NULL DEFAULT 'learner' CHECK (event_source IN ('learner', 'scenario', 'generator', 'system')),
	scenario_run_id TEXT,
	scenario_event_id TEXT,
	scenario_event_sequence INTEGER CHECK (scenario_event_sequence IS NULL OR scenario_event_sequence >= 0),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(resource_id) <> ''),
	CHECK (trim(account_id) <> ''),
	CHECK (trim(service_code) <> ''),
	CHECK (trim(usage_type) <> ''),
	CHECK (trim(operation) <> ''),
	CHECK (trim(region_code) <> ''),
	CHECK (usage_start_time < usage_end_time),
	CHECK (trim(usage_unit) <> ''),
	CHECK (
		event_source <> 'scenario'
		OR (
			scenario_run_id IS NOT NULL
			AND trim(scenario_run_id) <> ''
			AND scenario_event_id IS NOT NULL
			AND trim(scenario_event_id) <> ''
		)
	)
);

CREATE INDEX idx_usage_events_resource_time
ON usage_events (resource_id, usage_start_time, usage_end_time);

CREATE INDEX idx_usage_events_account_time
ON usage_events (account_id, usage_start_time, usage_end_time);

CREATE INDEX idx_usage_events_price_lookup
ON usage_events (service_code, usage_type, operation, region_code, usage_start_time);

CREATE INDEX idx_usage_events_scenario_provenance
ON usage_events (scenario_run_id, scenario_event_id);

PRAGMA user_version = 6;
