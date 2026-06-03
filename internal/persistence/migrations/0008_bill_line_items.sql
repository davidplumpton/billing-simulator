CREATE TABLE bill_line_items (
	id TEXT PRIMARY KEY,
	metering_record_id TEXT NOT NULL REFERENCES metering_records(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	usage_event_id TEXT NOT NULL REFERENCES usage_events(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	resource_id TEXT NOT NULL REFERENCES resources(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	billing_period_days INTEGER NOT NULL CHECK (billing_period_days > 0),
	payer_account_id TEXT NOT NULL,
	usage_account_id TEXT NOT NULL,
	service_code TEXT NOT NULL,
	service_name TEXT NOT NULL,
	product_family TEXT NOT NULL,
	usage_type TEXT NOT NULL,
	operation TEXT NOT NULL,
	region_code TEXT NOT NULL,
	line_item_type TEXT NOT NULL CHECK (line_item_type IN ('Usage', 'Credit', 'Tax', 'Fee', 'Refund')),
	usage_start_time TEXT NOT NULL,
	usage_end_time TEXT NOT NULL,
	usage_quantity_micros INTEGER NOT NULL CHECK (usage_quantity_micros > 0),
	usage_unit TEXT NOT NULL,
	pricing_unit TEXT NOT NULL,
	pricing_quantity_micros INTEGER NOT NULL CHECK (pricing_quantity_micros >= 0),
	unblended_rate_micros INTEGER NOT NULL CHECK (unblended_rate_micros >= 0),
	unblended_cost_micros INTEGER NOT NULL CHECK (unblended_cost_micros >= 0),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	price_catalog_sku TEXT NOT NULL,
	price_effective_date TEXT NOT NULL,
	tag_snapshot_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(tag_snapshot_json) AND json_type(tag_snapshot_json) = 'object'),
	description TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(metering_record_id) <> ''),
	CHECK (trim(usage_event_id) <> ''),
	CHECK (trim(resource_id) <> ''),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(payer_account_id) <> ''),
	CHECK (trim(usage_account_id) <> ''),
	CHECK (trim(service_code) <> ''),
	CHECK (trim(service_name) <> ''),
	CHECK (trim(product_family) <> ''),
	CHECK (trim(usage_type) <> ''),
	CHECK (trim(operation) <> ''),
	CHECK (trim(region_code) <> ''),
	CHECK (usage_start_time < usage_end_time),
	CHECK (trim(usage_unit) <> ''),
	CHECK (trim(pricing_unit) <> ''),
	CHECK (trim(price_catalog_sku) <> ''),
	CHECK (trim(price_effective_date) <> ''),
	CHECK (trim(description) <> ''),
	UNIQUE (metering_record_id),
	FOREIGN KEY (price_catalog_sku, price_effective_date)
		REFERENCES price_catalog_items(sku, effective_date)
		ON UPDATE CASCADE
		ON DELETE RESTRICT
);

CREATE INDEX idx_bill_line_items_period_account_service
ON bill_line_items (billing_period_start, billing_period_end, usage_account_id, service_code);

CREATE INDEX idx_bill_line_items_payer_period
ON bill_line_items (payer_account_id, billing_period_start, billing_period_end);

CREATE INDEX idx_bill_line_items_resource_time
ON bill_line_items (resource_id, usage_start_time, usage_end_time);

CREATE INDEX idx_bill_line_items_price_catalog
ON bill_line_items (price_catalog_sku, price_effective_date);

PRAGMA user_version = 8;
