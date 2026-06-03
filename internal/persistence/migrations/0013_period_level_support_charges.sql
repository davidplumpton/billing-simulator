DROP TRIGGER IF EXISTS reject_closed_bill_line_item_insert;
DROP TRIGGER IF EXISTS reject_closed_bill_line_item_update;
DROP TRIGGER IF EXISTS reject_closed_bill_line_item_delete;
DROP TRIGGER IF EXISTS reject_cross_period_bill_line_item_insert;
DROP TRIGGER IF EXISTS reject_cross_period_bill_line_item_update;

ALTER TABLE bill_line_items RENAME TO bill_line_items_old;

CREATE TABLE bill_line_items (
	id TEXT PRIMARY KEY,
	metering_record_id TEXT REFERENCES metering_records(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	usage_event_id TEXT REFERENCES usage_events(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	resource_id TEXT REFERENCES resources(id) ON UPDATE CASCADE ON DELETE RESTRICT,
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
	line_item_status TEXT NOT NULL DEFAULT 'estimated' CHECK (line_item_status IN ('estimated', 'final')),
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
	CHECK (metering_record_id IS NULL OR trim(metering_record_id) <> ''),
	CHECK (usage_event_id IS NULL OR trim(usage_event_id) <> ''),
	CHECK (resource_id IS NULL OR trim(resource_id) <> ''),
	CHECK (
		line_item_type <> 'Usage'
		OR (metering_record_id IS NOT NULL AND usage_event_id IS NOT NULL AND resource_id IS NOT NULL)
	),
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

INSERT INTO bill_line_items (
	id,
	metering_record_id,
	usage_event_id,
	resource_id,
	billing_period_start,
	billing_period_end,
	billing_period_days,
	payer_account_id,
	usage_account_id,
	service_code,
	service_name,
	product_family,
	usage_type,
	operation,
	region_code,
	line_item_type,
	line_item_status,
	usage_start_time,
	usage_end_time,
	usage_quantity_micros,
	usage_unit,
	pricing_unit,
	pricing_quantity_micros,
	unblended_rate_micros,
	unblended_cost_micros,
	currency_code,
	price_catalog_sku,
	price_effective_date,
	tag_snapshot_json,
	description,
	created_at
)
SELECT
	id,
	metering_record_id,
	usage_event_id,
	resource_id,
	billing_period_start,
	billing_period_end,
	billing_period_days,
	payer_account_id,
	usage_account_id,
	service_code,
	service_name,
	product_family,
	usage_type,
	operation,
	region_code,
	line_item_type,
	line_item_status,
	usage_start_time,
	usage_end_time,
	usage_quantity_micros,
	usage_unit,
	pricing_unit,
	pricing_quantity_micros,
	unblended_rate_micros,
	unblended_cost_micros,
	currency_code,
	price_catalog_sku,
	price_effective_date,
	tag_snapshot_json,
	description,
	created_at
FROM bill_line_items_old;

DROP TABLE bill_line_items_old;

CREATE INDEX idx_bill_line_items_period_account_service
ON bill_line_items (billing_period_start, billing_period_end, usage_account_id, service_code);

CREATE INDEX idx_bill_line_items_payer_period
ON bill_line_items (payer_account_id, billing_period_start, billing_period_end);

CREATE INDEX idx_bill_line_items_resource_time
ON bill_line_items (resource_id, usage_start_time, usage_end_time);

CREATE INDEX idx_bill_line_items_price_catalog
ON bill_line_items (price_catalog_sku, price_effective_date);

CREATE TABLE support_charge_sources (
	support_bill_line_item_id TEXT NOT NULL REFERENCES bill_line_items(id) ON UPDATE CASCADE ON DELETE CASCADE,
	source_bill_line_item_id TEXT NOT NULL REFERENCES bill_line_items(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	source_cost_micros INTEGER NOT NULL CHECK (source_cost_micros >= 0),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (support_bill_line_item_id, source_bill_line_item_id),
	CHECK (trim(support_bill_line_item_id) <> ''),
	CHECK (trim(source_bill_line_item_id) <> ''),
	CHECK (support_bill_line_item_id <> source_bill_line_item_id)
);

CREATE INDEX idx_support_charge_sources_source
ON support_charge_sources (source_bill_line_item_id);

CREATE TRIGGER reject_closed_bill_line_item_insert
BEFORE INSERT ON bill_line_items
WHEN EXISTS (
	SELECT 1
	FROM billing_period_closes c
	WHERE c.billing_period_start = NEW.billing_period_start
	  AND c.billing_period_end = NEW.billing_period_end
	  AND c.payer_account_id = NEW.payer_account_id
	  AND c.status = 'closed'
)
BEGIN
	SELECT RAISE(ABORT, 'billing period is closed for payer');
END;

CREATE TRIGGER reject_closed_bill_line_item_update
BEFORE UPDATE ON bill_line_items
WHEN EXISTS (
	SELECT 1
	FROM billing_period_closes c
	WHERE c.billing_period_start = OLD.billing_period_start
	  AND c.billing_period_end = OLD.billing_period_end
	  AND c.payer_account_id = OLD.payer_account_id
	  AND c.status = 'closed'
)
OR EXISTS (
	SELECT 1
	FROM billing_period_closes c
	WHERE c.billing_period_start = NEW.billing_period_start
	  AND c.billing_period_end = NEW.billing_period_end
	  AND c.payer_account_id = NEW.payer_account_id
	  AND c.status = 'closed'
)
BEGIN
	SELECT RAISE(ABORT, 'billing period is closed for payer');
END;

CREATE TRIGGER reject_closed_bill_line_item_delete
BEFORE DELETE ON bill_line_items
WHEN EXISTS (
	SELECT 1
	FROM billing_period_closes c
	WHERE c.billing_period_start = OLD.billing_period_start
	  AND c.billing_period_end = OLD.billing_period_end
	  AND c.payer_account_id = OLD.payer_account_id
	  AND c.status = 'closed'
)
BEGIN
	SELECT RAISE(ABORT, 'billing period is closed for payer');
END;

CREATE TRIGGER reject_cross_period_bill_line_item_insert
BEFORE INSERT ON bill_line_items
WHEN NEW.usage_start_time < (NEW.billing_period_start || 'T00:00:00Z')
  OR NEW.usage_end_time > (NEW.billing_period_end || 'T00:00:00Z')
BEGIN
	SELECT RAISE(ABORT, 'bill line item crosses billing period');
END;

CREATE TRIGGER reject_cross_period_bill_line_item_update
BEFORE UPDATE ON bill_line_items
WHEN NEW.usage_start_time < (NEW.billing_period_start || 'T00:00:00Z')
  OR NEW.usage_end_time > (NEW.billing_period_end || 'T00:00:00Z')
BEGIN
	SELECT RAISE(ABORT, 'bill line item crosses billing period');
END;

PRAGMA user_version = 13;
