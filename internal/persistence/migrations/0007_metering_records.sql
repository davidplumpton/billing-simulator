CREATE TABLE metering_records (
	id TEXT PRIMARY KEY,
	usage_event_id TEXT NOT NULL REFERENCES usage_events(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	resource_id TEXT NOT NULL REFERENCES resources(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	account_id TEXT NOT NULL,
	service_code TEXT NOT NULL,
	usage_type TEXT NOT NULL,
	operation TEXT NOT NULL,
	region_code TEXT NOT NULL,
	usage_start_time TEXT NOT NULL,
	usage_end_time TEXT NOT NULL,
	usage_quantity_micros INTEGER NOT NULL CHECK (usage_quantity_micros > 0),
	usage_unit TEXT NOT NULL,
	tag_snapshot_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(tag_snapshot_json) AND json_type(tag_snapshot_json) = 'object'),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(usage_event_id) <> ''),
	CHECK (trim(resource_id) <> ''),
	CHECK (trim(account_id) <> ''),
	CHECK (trim(service_code) <> ''),
	CHECK (trim(usage_type) <> ''),
	CHECK (trim(operation) <> ''),
	CHECK (trim(region_code) <> ''),
	CHECK (usage_start_time < usage_end_time),
	CHECK (trim(usage_unit) <> ''),
	UNIQUE (usage_event_id)
);

CREATE INDEX idx_metering_records_resource_time
ON metering_records (resource_id, usage_start_time, usage_end_time);

CREATE INDEX idx_metering_records_account_time
ON metering_records (account_id, usage_start_time, usage_end_time);

CREATE INDEX idx_metering_records_price_lookup
ON metering_records (service_code, usage_type, operation, region_code, usage_start_time);

PRAGMA user_version = 7;
