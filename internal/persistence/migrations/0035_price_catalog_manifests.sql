CREATE TABLE price_catalog_manifests (
	id TEXT PRIMARY KEY,
	source_url TEXT NOT NULL,
	fetch_date TEXT NOT NULL,
	effective_date TEXT NOT NULL,
	compatibility_key TEXT NOT NULL,
	compatibility_notes TEXT NOT NULL,
	is_active INTEGER NOT NULL DEFAULT 0 CHECK (is_active IN (0, 1)),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(source_url) <> ''),
	CHECK (trim(fetch_date) <> ''),
	CHECK (trim(effective_date) <> ''),
	CHECK (trim(compatibility_key) <> ''),
	CHECK (trim(compatibility_notes) <> '')
);

CREATE UNIQUE INDEX idx_price_catalog_manifests_active
ON price_catalog_manifests (is_active)
WHERE is_active = 1;

CREATE TABLE price_catalog_manifest_regions (
	catalog_id TEXT NOT NULL REFERENCES price_catalog_manifests(id) ON UPDATE CASCADE ON DELETE CASCADE,
	region_code TEXT NOT NULL,
	PRIMARY KEY (catalog_id, region_code),
	CHECK (trim(catalog_id) <> ''),
	CHECK (trim(region_code) <> '')
);

INSERT INTO price_catalog_manifests (
	id,
	source_url,
	fetch_date,
	effective_date,
	compatibility_key,
	compatibility_notes,
	is_active
) VALUES (
	'synthetic-2026-01-01',
	'embedded://internal/persistence/seeds/synthetic_price_catalog.csv',
	'2026-01-01',
	'2026-01-01',
	'scenario-v1',
	'Synthetic catalog supports packaged scenario services and regions from 2026-01-01 onward.',
	1
);

INSERT INTO price_catalog_manifest_regions (catalog_id, region_code) VALUES
	('synthetic-2026-01-01', 'global'),
	('synthetic-2026-01-01', 'us-east-1');

ALTER TABLE scenario_runs
ADD COLUMN price_catalog_id TEXT NOT NULL DEFAULT 'synthetic-2026-01-01';

ALTER TABLE scenario_runs
ADD COLUMN price_catalog_source_url TEXT NOT NULL DEFAULT 'embedded://internal/persistence/seeds/synthetic_price_catalog.csv';

ALTER TABLE scenario_runs
ADD COLUMN price_catalog_fetch_date TEXT NOT NULL DEFAULT '2026-01-01';

ALTER TABLE scenario_runs
ADD COLUMN price_catalog_effective_date TEXT NOT NULL DEFAULT '2026-01-01';

ALTER TABLE scenario_runs
ADD COLUMN price_catalog_supported_regions TEXT NOT NULL DEFAULT 'global,us-east-1';

ALTER TABLE scenario_runs
ADD COLUMN price_catalog_compatibility_key TEXT NOT NULL DEFAULT 'scenario-v1';

ALTER TABLE scenario_runs
ADD COLUMN price_catalog_compatibility_status TEXT NOT NULL DEFAULT 'compatible'
CHECK (price_catalog_compatibility_status IN ('compatible', 'incompatible'));

ALTER TABLE scenario_runs
ADD COLUMN price_catalog_compatibility_message TEXT NOT NULL DEFAULT 'Synthetic catalog supports packaged scenario services and regions from 2026-01-01 onward.';

PRAGMA user_version = 35;
