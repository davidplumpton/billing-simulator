CREATE TABLE account_lifecycle_events (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL REFERENCES organizations(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	account_id TEXT NOT NULL REFERENCES accounts(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	event_type TEXT NOT NULL CHECK (event_type IN ('created', 'moved', 'suspended', 'closed')),
	previous_parent_unit_id TEXT REFERENCES organization_units(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	new_parent_unit_id TEXT REFERENCES organization_units(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	previous_status TEXT CHECK (previous_status IS NULL OR previous_status IN ('active', 'suspended', 'closed')),
	new_status TEXT NOT NULL CHECK (new_status IN ('active', 'suspended', 'closed')),
	effective_at TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	event_source TEXT NOT NULL DEFAULT 'learner' CHECK (event_source IN ('learner', 'scenario', 'generator', 'system')),
	scenario_run_id TEXT,
	scenario_event_id TEXT,
	scenario_event_sequence INTEGER,
	CHECK (trim(id) <> ''),
	CHECK (trim(organization_id) <> ''),
	CHECK (trim(account_id) <> ''),
	CHECK (trim(event_type) <> ''),
	CHECK (trim(new_status) <> ''),
	CHECK (trim(effective_at) <> ''),
	CHECK (trim(event_source) <> ''),
	CHECK (scenario_run_id IS NULL OR trim(scenario_run_id) <> ''),
	CHECK (scenario_event_id IS NULL OR trim(scenario_event_id) <> ''),
	CHECK (scenario_event_sequence IS NULL OR scenario_event_sequence > 0),
	CHECK (
		(event_source = 'scenario' AND scenario_run_id IS NOT NULL AND scenario_event_id IS NOT NULL AND scenario_event_sequence IS NOT NULL)
		OR
		(event_source <> 'scenario' AND scenario_run_id IS NULL AND scenario_event_id IS NULL AND scenario_event_sequence IS NULL)
	)
);

CREATE INDEX idx_account_lifecycle_events_account_time
ON account_lifecycle_events (account_id, effective_at, id);

CREATE INDEX idx_account_lifecycle_events_org_time
ON account_lifecycle_events (organization_id, effective_at, id);

INSERT INTO account_lifecycle_events (
	id,
	organization_id,
	account_id,
	event_type,
	previous_parent_unit_id,
	new_parent_unit_id,
	previous_status,
	new_status,
	effective_at,
	created_at,
	event_source
)
SELECT
	'acctevt_' || id || '_created',
	organization_id,
	id,
	'created',
	NULL,
	parent_unit_id,
	NULL,
	status,
	joined_at,
	created_at,
	'system'
FROM accounts;

PRAGMA user_version = 20;
