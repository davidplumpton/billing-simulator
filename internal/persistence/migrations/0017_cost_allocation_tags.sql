CREATE TABLE cost_allocation_tag_keys (
	tag_key TEXT PRIMARY KEY,
	tag_type TEXT NOT NULL DEFAULT 'user-defined' CHECK (tag_type IN ('user-defined', 'aws-generated')),
	first_seen_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL,
	discovered_at TEXT NOT NULL,
	activation_status TEXT NOT NULL DEFAULT 'discovered' CHECK (activation_status IN ('discovered', 'active', 'deactivated')),
	activated_at TEXT,
	deactivated_at TEXT,
	cost_explorer_visible_at TEXT,
	cur_export_visible_at TEXT,
	event_source TEXT NOT NULL DEFAULT 'system' CHECK (event_source IN ('learner', 'scenario', 'generator', 'system')),
	scenario_run_id TEXT,
	scenario_event_id TEXT,
	scenario_event_sequence INTEGER CHECK (scenario_event_sequence IS NULL OR scenario_event_sequence >= 0),
	CHECK (trim(tag_key) <> ''),
	CHECK (first_seen_at <= last_seen_at),
	CHECK (discovered_at >= first_seen_at),
	CHECK (activated_at IS NULL OR activated_at >= discovered_at),
	CHECK (deactivated_at IS NULL OR activated_at IS NOT NULL),
	CHECK (deactivated_at IS NULL OR deactivated_at >= activated_at),
	CHECK (cost_explorer_visible_at IS NULL OR activated_at IS NOT NULL),
	CHECK (cur_export_visible_at IS NULL OR activated_at IS NOT NULL),
	CHECK (cost_explorer_visible_at IS NULL OR cost_explorer_visible_at >= activated_at),
	CHECK (cur_export_visible_at IS NULL OR cur_export_visible_at >= activated_at),
	CHECK (activation_status <> 'active' OR activated_at IS NOT NULL),
	CHECK (activation_status <> 'deactivated' OR deactivated_at IS NOT NULL),
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

CREATE INDEX idx_cost_allocation_tag_keys_status
ON cost_allocation_tag_keys (activation_status, cost_explorer_visible_at);

CREATE INDEX idx_cost_allocation_tag_keys_scenario_provenance
ON cost_allocation_tag_keys (scenario_run_id, scenario_event_id);

CREATE TABLE cost_allocation_tag_inventory (
	tag_key TEXT NOT NULL REFERENCES cost_allocation_tag_keys(tag_key) ON UPDATE CASCADE ON DELETE CASCADE,
	tag_value TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL,
	resource_count INTEGER NOT NULL DEFAULT 0 CHECK (resource_count >= 0),
	PRIMARY KEY (tag_key, tag_value),
	CHECK (trim(tag_key) <> ''),
	CHECK (first_seen_at <= last_seen_at)
);

CREATE INDEX idx_cost_allocation_tag_inventory_key_count
ON cost_allocation_tag_inventory (tag_key, resource_count DESC);

CREATE TABLE cost_allocation_tag_activation_events (
	id TEXT PRIMARY KEY,
	tag_key TEXT NOT NULL REFERENCES cost_allocation_tag_keys(tag_key) ON UPDATE CASCADE ON DELETE RESTRICT,
	action TEXT NOT NULL CHECK (action IN ('activate', 'deactivate')),
	requested_at TEXT NOT NULL,
	effective_at TEXT NOT NULL,
	cost_explorer_visible_at TEXT,
	cur_export_visible_at TEXT,
	event_source TEXT NOT NULL DEFAULT 'learner' CHECK (event_source IN ('learner', 'scenario', 'generator', 'system')),
	scenario_run_id TEXT,
	scenario_event_id TEXT,
	scenario_event_sequence INTEGER CHECK (scenario_event_sequence IS NULL OR scenario_event_sequence >= 0),
	CHECK (trim(id) <> ''),
	CHECK (trim(tag_key) <> ''),
	CHECK (cost_explorer_visible_at IS NULL OR cost_explorer_visible_at >= effective_at),
	CHECK (cur_export_visible_at IS NULL OR cur_export_visible_at >= effective_at),
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

CREATE INDEX idx_cost_allocation_tag_activation_events_key_time
ON cost_allocation_tag_activation_events (tag_key, requested_at DESC);

CREATE VIEW discovered_cost_allocation_tag_keys AS
SELECT
	tag_key,
	tag_type,
	first_seen_at,
	last_seen_at,
	discovered_at,
	activation_status,
	activated_at,
	deactivated_at,
	cost_explorer_visible_at,
	cur_export_visible_at
FROM cost_allocation_tag_keys;

CREATE VIEW active_cost_allocation_tag_keys AS
SELECT
	tag_key,
	tag_type,
	first_seen_at,
	last_seen_at,
	discovered_at,
	activated_at,
	cost_explorer_visible_at,
	cur_export_visible_at
FROM cost_allocation_tag_keys
WHERE activation_status = 'active';

PRAGMA user_version = 17;
