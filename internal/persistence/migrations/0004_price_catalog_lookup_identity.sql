CREATE UNIQUE INDEX idx_price_catalog_items_lookup_identity
ON price_catalog_items (
	service_code,
	usage_type,
	operation,
	region_code,
	effective_date
);

PRAGMA user_version = 4;
