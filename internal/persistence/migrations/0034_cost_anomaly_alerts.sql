CREATE TABLE cost_anomaly_alerts (
	id TEXT PRIMARY KEY,
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	baseline_period_start TEXT NOT NULL,
	baseline_period_end TEXT NOT NULL,
	dimension_type TEXT NOT NULL CHECK (dimension_type IN ('service', 'account', 'tag', 'cost_category')),
	dimension_key TEXT NOT NULL,
	dimension_value TEXT NOT NULL,
	dimension_label TEXT NOT NULL,
	payer_account_id TEXT NOT NULL,
	line_item_status TEXT NOT NULL CHECK (line_item_status IN ('estimated', 'final')),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	spike_kind TEXT NOT NULL CHECK (spike_kind IN ('increase', 'new_spend')),
	current_cost_micros INTEGER NOT NULL CHECK (current_cost_micros >= 0),
	baseline_cost_micros INTEGER NOT NULL CHECK (baseline_cost_micros >= 0),
	increase_cost_micros INTEGER NOT NULL CHECK (increase_cost_micros >= 0),
	current_cost_basis_points INTEGER NOT NULL CHECK (current_cost_basis_points >= 0),
	current_line_item_count INTEGER NOT NULL CHECK (current_line_item_count >= 0),
	baseline_line_item_count INTEGER NOT NULL CHECK (baseline_line_item_count >= 0),
	threshold_basis_points INTEGER NOT NULL CHECK (threshold_basis_points > 0),
	minimum_current_cost_micros INTEGER NOT NULL CHECK (minimum_current_cost_micros >= 0),
	message TEXT NOT NULL,
	first_detected_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	last_observed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	UNIQUE (
		billing_period_start,
		billing_period_end,
		baseline_period_start,
		baseline_period_end,
		dimension_type,
		dimension_key,
		dimension_value,
		payer_account_id,
		line_item_status,
		currency_code
	),
	CHECK (trim(id) <> ''),
	CHECK (billing_period_start < billing_period_end),
	CHECK (baseline_period_start < baseline_period_end),
	CHECK (baseline_period_end <= billing_period_start),
	CHECK (trim(dimension_key) <> ''),
	CHECK (trim(dimension_value) <> ''),
	CHECK (trim(dimension_label) <> ''),
	CHECK (trim(payer_account_id) <> ''),
	CHECK (trim(message) <> '')
);

CREATE INDEX idx_cost_anomaly_alerts_period
ON cost_anomaly_alerts (
	billing_period_start,
	billing_period_end,
	dimension_type,
	current_cost_micros
);

CREATE INDEX idx_cost_anomaly_alerts_payer
ON cost_anomaly_alerts (
	payer_account_id,
	billing_period_start,
	billing_period_end
);

PRAGMA user_version = 34;
