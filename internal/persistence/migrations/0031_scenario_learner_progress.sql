CREATE TABLE scenario_learner_progress (
	scenario_run_id TEXT PRIMARY KEY REFERENCES scenario_runs(id) ON UPDATE CASCADE ON DELETE CASCADE,
	definition_name TEXT NOT NULL,
	objective TEXT NOT NULL DEFAULT '',
	current_objective_state TEXT NOT NULL CHECK (current_objective_state IN ('in_progress', 'completed', 'needs_review', 'failed')),
	current_objective TEXT NOT NULL DEFAULT '',
	actions_total INTEGER NOT NULL DEFAULT 0 CHECK (actions_total >= 0),
	actions_completed INTEGER NOT NULL DEFAULT 0 CHECK (actions_completed >= 0),
	checks_total INTEGER NOT NULL DEFAULT 0 CHECK (checks_total >= 0),
	checks_passed INTEGER NOT NULL DEFAULT 0 CHECK (checks_passed >= 0),
	checks_failed INTEGER NOT NULL DEFAULT 0 CHECK (checks_failed >= 0),
	started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	completed_at TEXT,
	CHECK (trim(scenario_run_id) <> ''),
	CHECK (trim(definition_name) <> ''),
	CHECK (actions_completed <= actions_total),
	CHECK (checks_passed + checks_failed <= checks_total),
	CHECK (completed_at IS NULL OR completed_at >= started_at)
);

CREATE INDEX idx_scenario_learner_progress_definition_updated
ON scenario_learner_progress (definition_name, updated_at);

CREATE TABLE scenario_learner_actions (
	id TEXT PRIMARY KEY,
	scenario_run_id TEXT NOT NULL REFERENCES scenario_learner_progress(scenario_run_id) ON UPDATE CASCADE ON DELETE CASCADE,
	action_id TEXT NOT NULL,
	action_sequence INTEGER NOT NULL CHECK (action_sequence > 0),
	action_type TEXT NOT NULL,
	action_status TEXT NOT NULL CHECK (action_status IN ('completed', 'failed')),
	completed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	evidence TEXT NOT NULL DEFAULT '',
	error_message TEXT NOT NULL DEFAULT '',
	CHECK (trim(id) <> ''),
	CHECK (trim(scenario_run_id) <> ''),
	CHECK (trim(action_id) <> ''),
	CHECK (trim(action_type) <> ''),
	UNIQUE (scenario_run_id, action_id)
);

CREATE INDEX idx_scenario_learner_actions_run_sequence
ON scenario_learner_actions (scenario_run_id, action_sequence);

CREATE TABLE scenario_learner_check_results (
	id TEXT PRIMARY KEY,
	scenario_run_id TEXT NOT NULL REFERENCES scenario_learner_progress(scenario_run_id) ON UPDATE CASCADE ON DELETE CASCADE,
	check_id TEXT NOT NULL,
	check_sequence INTEGER NOT NULL CHECK (check_sequence > 0),
	check_type TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('passed', 'failed')),
	expected TEXT NOT NULL DEFAULT '',
	actual TEXT NOT NULL DEFAULT '',
	message TEXT NOT NULL DEFAULT '',
	evaluated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(scenario_run_id) <> ''),
	CHECK (trim(check_id) <> ''),
	CHECK (trim(check_type) <> ''),
	UNIQUE (scenario_run_id, check_id)
);

CREATE INDEX idx_scenario_learner_check_results_run_sequence
ON scenario_learner_check_results (scenario_run_id, check_sequence);

PRAGMA user_version = 31;
