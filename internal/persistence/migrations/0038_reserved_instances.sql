CREATE TABLE reserved_instance_purchases (
	id TEXT PRIMARY KEY,
	payer_account_id TEXT NOT NULL,
	owner_account_id TEXT NOT NULL,
	service_code TEXT NOT NULL,
	service_name TEXT NOT NULL,
	product_family TEXT NOT NULL DEFAULT 'Reserved Instance',
	usage_type TEXT NOT NULL,
	operation TEXT NOT NULL,
	region_code TEXT NOT NULL,
	instance_count INTEGER NOT NULL CHECK (instance_count > 0),
	sharing_scope TEXT NOT NULL DEFAULT 'organization' CHECK (sharing_scope IN ('organization', 'owner_account')),
	term_start_time TEXT NOT NULL,
	term_end_time TEXT NOT NULL,
	upfront_fee_micros INTEGER NOT NULL DEFAULT 0 CHECK (upfront_fee_micros >= 0),
	monthly_recurring_fee_micros INTEGER NOT NULL DEFAULT 0 CHECK (monthly_recurring_fee_micros >= 0),
	currency_code TEXT NOT NULL DEFAULT 'USD' CHECK (length(currency_code) = 3),
	price_catalog_sku TEXT NOT NULL,
	price_effective_date TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'retired')),
	description TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(payer_account_id) <> ''),
	CHECK (trim(owner_account_id) <> ''),
	CHECK (trim(service_code) <> ''),
	CHECK (trim(service_name) <> ''),
	CHECK (trim(product_family) <> ''),
	CHECK (trim(usage_type) <> ''),
	CHECK (trim(operation) <> ''),
	CHECK (trim(region_code) <> ''),
	CHECK (term_start_time < term_end_time),
	CHECK (trim(price_catalog_sku) <> ''),
	CHECK (trim(price_effective_date) <> ''),
	FOREIGN KEY (price_catalog_sku, price_effective_date)
		REFERENCES price_catalog_items(sku, effective_date)
		ON UPDATE CASCADE
		ON DELETE RESTRICT
);

CREATE INDEX idx_reserved_instance_purchases_payer_period
ON reserved_instance_purchases (payer_account_id, status, term_start_time, term_end_time);

CREATE INDEX idx_reserved_instance_purchases_dimensions
ON reserved_instance_purchases (service_code, usage_type, operation, region_code);

CREATE TABLE reserved_instance_line_item_sources (
	bill_line_item_id TEXT NOT NULL REFERENCES bill_line_items(id) ON UPDATE CASCADE ON DELETE CASCADE,
	reserved_instance_id TEXT NOT NULL REFERENCES reserved_instance_purchases(id) ON UPDATE CASCADE ON DELETE CASCADE,
	source_bill_line_item_id TEXT REFERENCES bill_line_items(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	line_item_kind TEXT NOT NULL CHECK (line_item_kind IN ('upfront_fee', 'recurring_fee', 'coverage_credit')),
	covered_quantity_micros INTEGER NOT NULL DEFAULT 0 CHECK (covered_quantity_micros >= 0),
	covered_cost_micros INTEGER NOT NULL DEFAULT 0 CHECK (covered_cost_micros >= 0),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (bill_line_item_id, reserved_instance_id, line_item_kind),
	CHECK (trim(bill_line_item_id) <> ''),
	CHECK (trim(reserved_instance_id) <> ''),
	CHECK (
		(line_item_kind = 'coverage_credit' AND source_bill_line_item_id IS NOT NULL AND trim(source_bill_line_item_id) <> '')
		OR (line_item_kind IN ('upfront_fee', 'recurring_fee') AND source_bill_line_item_id IS NULL)
	)
);

CREATE INDEX idx_reserved_instance_line_item_sources_reserved_instance
ON reserved_instance_line_item_sources (reserved_instance_id, line_item_kind);

CREATE INDEX idx_reserved_instance_line_item_sources_source
ON reserved_instance_line_item_sources (source_bill_line_item_id);

PRAGMA user_version = 38;
