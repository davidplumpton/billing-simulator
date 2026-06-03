ALTER TABLE price_catalog_items RENAME TO price_catalog_items_old;

CREATE TABLE price_catalog_items (
	sku TEXT NOT NULL,
	service_code TEXT NOT NULL,
	service_name TEXT NOT NULL,
	product_family TEXT NOT NULL,
	usage_type TEXT NOT NULL,
	operation TEXT NOT NULL,
	region_code TEXT NOT NULL,
	unit TEXT NOT NULL,
	rate_micros INTEGER NOT NULL CHECK (rate_micros > 0),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	effective_date TEXT NOT NULL,
	price_source TEXT NOT NULL CHECK (price_source IN ('synthetic', 'aws_price_list_snapshot', 'instructor_override')),
	pricing_formula TEXT NOT NULL,
	notes TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (sku, effective_date)
);

INSERT INTO price_catalog_items (
	sku,
	service_code,
	service_name,
	product_family,
	usage_type,
	operation,
	region_code,
	unit,
	rate_micros,
	currency_code,
	effective_date,
	price_source,
	pricing_formula,
	notes,
	created_at
)
SELECT
	sku,
	service_code,
	service_name,
	product_family,
	usage_type,
	operation,
	region_code,
	unit,
	rate_micros,
	currency_code,
	effective_date,
	price_source,
	pricing_formula,
	notes,
	created_at
FROM price_catalog_items_old;

DROP TABLE price_catalog_items_old;

CREATE INDEX idx_price_catalog_items_service_region
ON price_catalog_items (service_code, region_code);

CREATE UNIQUE INDEX idx_price_catalog_items_lookup_identity
ON price_catalog_items (
	service_code,
	usage_type,
	operation,
	region_code,
	effective_date
);

PRAGMA user_version = 5;
