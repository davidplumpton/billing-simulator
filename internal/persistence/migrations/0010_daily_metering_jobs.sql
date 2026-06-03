ALTER TABLE bill_line_items
ADD COLUMN line_item_status TEXT NOT NULL DEFAULT 'estimated'
	CHECK (line_item_status IN ('estimated', 'final'));

CREATE TABLE daily_metering_job_runs (
	id TEXT PRIMARY KEY,
	trigger_source TEXT NOT NULL CHECK (trigger_source IN ('on_demand', 'clock_advance')),
	status TEXT NOT NULL CHECK (status IN ('succeeded')),
	clock_time_utc TEXT NOT NULL,
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	payer_account_id TEXT NOT NULL,
	metering_records_created INTEGER NOT NULL CHECK (metering_records_created >= 0),
	bill_line_items_created INTEGER NOT NULL CHECK (bill_line_items_created >= 0),
	summaries_refreshed INTEGER NOT NULL CHECK (summaries_refreshed >= 0),
	started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	completed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(clock_time_utc) <> ''),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(payer_account_id) <> '')
);

CREATE INDEX idx_daily_metering_job_runs_completed
ON daily_metering_job_runs (completed_at DESC, id DESC);

CREATE TABLE billing_period_service_summaries (
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	payer_account_id TEXT NOT NULL,
	usage_account_id TEXT NOT NULL,
	service_code TEXT NOT NULL,
	line_item_status TEXT NOT NULL CHECK (line_item_status IN ('estimated', 'final')),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	line_item_count INTEGER NOT NULL CHECK (line_item_count >= 0),
	unblended_cost_micros INTEGER NOT NULL CHECK (unblended_cost_micros >= 0),
	refreshed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (
		billing_period_start,
		billing_period_end,
		payer_account_id,
		usage_account_id,
		service_code,
		line_item_status,
		currency_code
	),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(payer_account_id) <> ''),
	CHECK (trim(usage_account_id) <> ''),
	CHECK (trim(service_code) <> '')
);

CREATE INDEX idx_billing_period_service_summaries_period
ON billing_period_service_summaries (billing_period_start, billing_period_end, payer_account_id, service_code);

PRAGMA user_version = 10;
