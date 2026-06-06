CREATE TABLE daily_cost_summary (
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	usage_date TEXT NOT NULL,
	payer_account_id TEXT NOT NULL,
	usage_account_id TEXT NOT NULL,
	service_code TEXT NOT NULL,
	service_name TEXT NOT NULL,
	line_item_type TEXT NOT NULL CHECK (line_item_type IN ('Usage', 'Credit', 'Tax', 'Fee', 'Refund')),
	line_item_status TEXT NOT NULL CHECK (line_item_status IN ('estimated', 'final')),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	line_item_count INTEGER NOT NULL CHECK (line_item_count >= 0),
	usage_quantity_micros INTEGER NOT NULL CHECK (usage_quantity_micros >= 0),
	unblended_cost_micros INTEGER NOT NULL CHECK (unblended_cost_micros >= 0),
	refreshed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (
		billing_period_start,
		billing_period_end,
		usage_date,
		payer_account_id,
		usage_account_id,
		service_code,
		line_item_type,
		line_item_status,
		currency_code
	),
	CHECK (billing_period_start < billing_period_end),
	CHECK (usage_date >= billing_period_start AND usage_date < billing_period_end),
	CHECK (trim(payer_account_id) <> ''),
	CHECK (trim(usage_account_id) <> ''),
	CHECK (trim(service_code) <> ''),
	CHECK (trim(service_name) <> '')
);

CREATE INDEX idx_daily_cost_summary_period_service
ON daily_cost_summary (
	billing_period_start,
	billing_period_end,
	usage_date,
	payer_account_id,
	service_code
);

CREATE TABLE monthly_account_service_summary (
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	payer_account_id TEXT NOT NULL,
	usage_account_id TEXT NOT NULL,
	service_code TEXT NOT NULL,
	service_name TEXT NOT NULL,
	line_item_type TEXT NOT NULL CHECK (line_item_type IN ('Usage', 'Credit', 'Tax', 'Fee', 'Refund')),
	line_item_status TEXT NOT NULL CHECK (line_item_status IN ('estimated', 'final')),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	line_item_count INTEGER NOT NULL CHECK (line_item_count >= 0),
	usage_quantity_micros INTEGER NOT NULL CHECK (usage_quantity_micros >= 0),
	unblended_cost_micros INTEGER NOT NULL CHECK (unblended_cost_micros >= 0),
	refreshed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (
		billing_period_start,
		billing_period_end,
		payer_account_id,
		usage_account_id,
		service_code,
		line_item_type,
		line_item_status,
		currency_code
	),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(payer_account_id) <> ''),
	CHECK (trim(usage_account_id) <> ''),
	CHECK (trim(service_code) <> ''),
	CHECK (trim(service_name) <> '')
);

CREATE INDEX idx_monthly_account_service_summary_account
ON monthly_account_service_summary (
	billing_period_start,
	billing_period_end,
	usage_account_id,
	service_code
);

CREATE TABLE tag_coverage_summary (
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	tag_key TEXT NOT NULL,
	dimension TEXT NOT NULL CHECK (dimension IN ('key', 'account', 'service')),
	dimension_value TEXT NOT NULL,
	dimension_label TEXT NOT NULL,
	activation_status TEXT NOT NULL CHECK (activation_status IN ('discovered', 'active', 'deactivated')),
	cost_explorer_visible_at TEXT,
	currency_code TEXT NOT NULL CHECK (currency_code = 'mixed' OR length(currency_code) = 3),
	line_item_count INTEGER NOT NULL CHECK (line_item_count >= 0),
	resource_count INTEGER NOT NULL CHECK (resource_count >= 0),
	tagged_line_item_count INTEGER NOT NULL CHECK (tagged_line_item_count >= 0),
	tagged_resource_count INTEGER NOT NULL CHECK (tagged_resource_count >= 0),
	untagged_line_item_count INTEGER NOT NULL CHECK (untagged_line_item_count >= 0),
	untagged_resource_count INTEGER NOT NULL CHECK (untagged_resource_count >= 0),
	case_mismatch_line_item_count INTEGER NOT NULL CHECK (case_mismatch_line_item_count >= 0),
	case_mismatch_resource_count INTEGER NOT NULL CHECK (case_mismatch_resource_count >= 0),
	total_cost_micros INTEGER NOT NULL CHECK (total_cost_micros >= 0),
	tagged_cost_micros INTEGER NOT NULL CHECK (tagged_cost_micros >= 0),
	untagged_cost_micros INTEGER NOT NULL CHECK (untagged_cost_micros >= 0),
	case_mismatch_cost_micros INTEGER NOT NULL CHECK (case_mismatch_cost_micros >= 0),
	case_mismatch_keys_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(case_mismatch_keys_json) AND json_type(case_mismatch_keys_json) = 'array'),
	refreshed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (
		billing_period_start,
		billing_period_end,
		tag_key,
		dimension,
		dimension_value
	),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(tag_key) <> ''),
	CHECK (trim(dimension_value) <> ''),
	CHECK (trim(dimension_label) <> '')
);

CREATE INDEX idx_tag_coverage_summary_dimension
ON tag_coverage_summary (
	billing_period_start,
	billing_period_end,
	dimension,
	tag_key
);

CREATE TABLE cost_category_summary (
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	cost_category_id TEXT NOT NULL REFERENCES cost_categories(id) ON UPDATE CASCADE ON DELETE CASCADE,
	cost_category_name TEXT NOT NULL,
	assigned_value TEXT NOT NULL,
	payer_account_id TEXT NOT NULL,
	usage_account_id TEXT NOT NULL,
	line_item_status TEXT NOT NULL CHECK (line_item_status IN ('estimated', 'final')),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	line_item_count INTEGER NOT NULL CHECK (line_item_count >= 0),
	unblended_cost_micros INTEGER NOT NULL CHECK (unblended_cost_micros >= 0),
	refreshed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (
		billing_period_start,
		billing_period_end,
		cost_category_id,
		assigned_value,
		payer_account_id,
		usage_account_id,
		line_item_status,
		currency_code
	),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(cost_category_name) <> ''),
	CHECK (trim(assigned_value) <> ''),
	CHECK (trim(payer_account_id) <> ''),
	CHECK (trim(usage_account_id) <> '')
);

CREATE INDEX idx_cost_category_summary_period_value
ON cost_category_summary (
	cost_category_id,
	billing_period_start,
	billing_period_end,
	assigned_value
);

PRAGMA user_version = 25;
