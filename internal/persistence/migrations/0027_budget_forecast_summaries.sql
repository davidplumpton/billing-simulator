CREATE TABLE budget_forecast_summaries (
	budget_id TEXT NOT NULL REFERENCES budgets(id) ON UPDATE CASCADE ON DELETE CASCADE,
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	current_time_utc TEXT NOT NULL,
	elapsed_days INTEGER NOT NULL CHECK (elapsed_days >= 0),
	period_days INTEGER NOT NULL CHECK (period_days > 0),
	actual_cost_micros INTEGER NOT NULL CHECK (actual_cost_micros >= 0),
	run_rate_forecast_micros INTEGER NOT NULL CHECK (run_rate_forecast_micros >= 0),
	scheduled_event_cost_micros INTEGER NOT NULL CHECK (scheduled_event_cost_micros >= 0),
	forecast_cost_micros INTEGER NOT NULL CHECK (forecast_cost_micros >= 0),
	line_item_count INTEGER NOT NULL CHECK (line_item_count >= 0),
	scheduled_usage_event_count INTEGER NOT NULL CHECK (scheduled_usage_event_count >= 0),
	currency_code TEXT NOT NULL DEFAULT 'USD' CHECK (length(currency_code) = 3),
	refreshed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (budget_id, billing_period_start, billing_period_end),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(budget_id) <> ''),
	CHECK (trim(current_time_utc) <> '')
);

CREATE INDEX idx_budget_forecast_summaries_period
ON budget_forecast_summaries (
	billing_period_start,
	billing_period_end,
	forecast_cost_micros
);

PRAGMA user_version = 27;
