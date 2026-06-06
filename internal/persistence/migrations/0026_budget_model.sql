CREATE TABLE budgets (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	budget_amount_micros INTEGER NOT NULL CHECK (budget_amount_micros > 0),
	currency_code TEXT NOT NULL DEFAULT 'USD' CHECK (length(currency_code) = 3),
	scope_type TEXT NOT NULL CHECK (scope_type IN ('account', 'service', 'tag', 'cost_category')),
	scope_key TEXT,
	scope_value TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(id) <> ''),
	CHECK (trim(name) <> ''),
	CHECK (trim(currency_code) <> ''),
	CHECK (trim(scope_value) <> ''),
	CHECK (
		(scope_type IN ('account', 'service') AND (scope_key IS NULL OR trim(scope_key) = ''))
		OR
		(scope_type IN ('tag', 'cost_category') AND scope_key IS NOT NULL AND trim(scope_key) <> '')
	)
);

CREATE UNIQUE INDEX idx_budgets_period_name
ON budgets (
	billing_period_start,
	billing_period_end,
	lower(name)
);

CREATE INDEX idx_budgets_status_period
ON budgets (
	status,
	billing_period_start,
	billing_period_end
);

CREATE INDEX idx_budgets_scope
ON budgets (
	scope_type,
	scope_key,
	scope_value
);

CREATE TABLE budget_thresholds (
	id TEXT PRIMARY KEY,
	budget_id TEXT NOT NULL REFERENCES budgets(id) ON UPDATE CASCADE ON DELETE CASCADE,
	threshold_type TEXT NOT NULL CHECK (threshold_type IN ('actual', 'forecast')),
	threshold_basis_points INTEGER NOT NULL CHECK (threshold_basis_points > 0 AND threshold_basis_points <= 100000),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	UNIQUE (budget_id, threshold_type, threshold_basis_points)
);

CREATE INDEX idx_budget_thresholds_budget_type
ON budget_thresholds (
	budget_id,
	threshold_type,
	threshold_basis_points
);

PRAGMA user_version = 26;
