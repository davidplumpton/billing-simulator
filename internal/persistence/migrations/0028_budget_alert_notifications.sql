CREATE TABLE budget_alert_notifications (
	id TEXT PRIMARY KEY,
	budget_id TEXT NOT NULL REFERENCES budgets(id) ON UPDATE CASCADE ON DELETE CASCADE,
	budget_threshold_id TEXT NOT NULL REFERENCES budget_thresholds(id) ON UPDATE CASCADE ON DELETE CASCADE,
	budget_name TEXT NOT NULL,
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	budget_amount_micros INTEGER NOT NULL CHECK (budget_amount_micros > 0),
	currency_code TEXT NOT NULL DEFAULT 'USD' CHECK (length(currency_code) = 3),
	threshold_type TEXT NOT NULL CHECK (threshold_type IN ('actual', 'forecast')),
	threshold_basis_points INTEGER NOT NULL CHECK (threshold_basis_points > 0 AND threshold_basis_points <= 100000),
	threshold_amount_micros INTEGER NOT NULL CHECK (threshold_amount_micros >= 0),
	spend_micros INTEGER NOT NULL CHECK (spend_micros >= 0),
	percent_used_basis_points INTEGER NOT NULL CHECK (percent_used_basis_points >= 0),
	line_item_count INTEGER NOT NULL CHECK (line_item_count >= 0),
	notification_channel TEXT NOT NULL DEFAULT 'in_app' CHECK (notification_channel = 'in_app'),
	message TEXT NOT NULL,
	first_triggered_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	last_observed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(id) <> ''),
	CHECK (trim(budget_id) <> ''),
	CHECK (trim(budget_threshold_id) <> ''),
	CHECK (trim(budget_name) <> ''),
	CHECK (trim(message) <> ''),
	UNIQUE (budget_threshold_id, billing_period_start, billing_period_end)
);

CREATE INDEX idx_budget_alert_notifications_period
ON budget_alert_notifications (
	billing_period_start,
	billing_period_end,
	first_triggered_at
);

CREATE INDEX idx_budget_alert_notifications_budget
ON budget_alert_notifications (
	budget_id,
	budget_threshold_id
);

PRAGMA user_version = 28;
