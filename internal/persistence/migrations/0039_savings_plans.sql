CREATE TABLE savings_plan_purchases (
	id TEXT PRIMARY KEY,
	payer_account_id TEXT NOT NULL,
	owner_account_id TEXT NOT NULL,
	plan_type TEXT NOT NULL DEFAULT 'compute' CHECK (plan_type IN ('compute')),
	service_code TEXT NOT NULL DEFAULT 'AmazonEC2',
	service_name TEXT NOT NULL,
	product_family TEXT NOT NULL DEFAULT 'Savings Plan',
	reference_usage_type TEXT NOT NULL,
	operation TEXT NOT NULL DEFAULT 'RunInstances',
	region_code TEXT NOT NULL,
	sharing_scope TEXT NOT NULL DEFAULT 'organization' CHECK (sharing_scope IN ('organization', 'owner_account')),
	term_start_time TEXT NOT NULL,
	term_end_time TEXT NOT NULL,
	hourly_commitment_micros INTEGER NOT NULL CHECK (hourly_commitment_micros > 0),
	upfront_fee_micros INTEGER NOT NULL DEFAULT 0 CHECK (upfront_fee_micros >= 0),
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
	CHECK (trim(reference_usage_type) <> ''),
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

CREATE INDEX idx_savings_plan_purchases_payer_period
ON savings_plan_purchases (payer_account_id, status, term_start_time, term_end_time);

CREATE INDEX idx_savings_plan_purchases_dimensions
ON savings_plan_purchases (service_code, operation, region_code);

CREATE TABLE savings_plan_line_item_sources (
	bill_line_item_id TEXT NOT NULL REFERENCES bill_line_items(id) ON UPDATE CASCADE ON DELETE CASCADE,
	savings_plan_id TEXT NOT NULL REFERENCES savings_plan_purchases(id) ON UPDATE CASCADE ON DELETE CASCADE,
	source_bill_line_item_id TEXT REFERENCES bill_line_items(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	line_item_kind TEXT NOT NULL CHECK (line_item_kind IN ('upfront_fee', 'recurring_fee', 'negation')),
	covered_quantity_micros INTEGER NOT NULL DEFAULT 0 CHECK (covered_quantity_micros >= 0),
	covered_cost_micros INTEGER NOT NULL DEFAULT 0 CHECK (covered_cost_micros >= 0),
	amortized_commitment_cost_micros INTEGER NOT NULL DEFAULT 0 CHECK (amortized_commitment_cost_micros >= 0),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (bill_line_item_id, savings_plan_id, line_item_kind),
	CHECK (trim(bill_line_item_id) <> ''),
	CHECK (trim(savings_plan_id) <> ''),
	CHECK (
		(line_item_kind = 'negation' AND source_bill_line_item_id IS NOT NULL AND trim(source_bill_line_item_id) <> '')
		OR (line_item_kind IN ('upfront_fee', 'recurring_fee') AND source_bill_line_item_id IS NULL)
	)
);

CREATE INDEX idx_savings_plan_line_item_sources_plan
ON savings_plan_line_item_sources (savings_plan_id, line_item_kind);

CREATE INDEX idx_savings_plan_line_item_sources_source
ON savings_plan_line_item_sources (source_bill_line_item_id);

PRAGMA user_version = 39;
