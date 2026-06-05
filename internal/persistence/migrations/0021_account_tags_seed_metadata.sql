CREATE TABLE account_tags (
	id TEXT PRIMARY KEY,
	account_id TEXT NOT NULL REFERENCES accounts(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	tag_key TEXT NOT NULL,
	tag_value TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	removed_at TEXT,
	event_source TEXT NOT NULL DEFAULT 'system' CHECK (event_source IN ('learner', 'scenario', 'generator', 'system')),
	scenario_run_id TEXT,
	scenario_event_id TEXT,
	scenario_event_sequence INTEGER CHECK (scenario_event_sequence IS NULL OR scenario_event_sequence >= 0),
	CHECK (trim(id) <> ''),
	CHECK (trim(account_id) <> ''),
	CHECK (trim(tag_key) <> ''),
	CHECK (removed_at IS NULL OR removed_at > applied_at),
	CHECK (
		event_source <> 'scenario'
		OR (
			scenario_run_id IS NOT NULL
			AND trim(scenario_run_id) <> ''
			AND scenario_event_id IS NOT NULL
			AND trim(scenario_event_id) <> ''
		)
	)
);

CREATE INDEX idx_account_tags_account
ON account_tags (account_id);

CREATE INDEX idx_account_tags_key_value
ON account_tags (tag_key, tag_value);

CREATE INDEX idx_account_tags_scenario_provenance
ON account_tags (scenario_run_id, scenario_event_id);

CREATE UNIQUE INDEX idx_account_tags_one_active_value
ON account_tags (account_id, tag_key)
WHERE removed_at IS NULL;

WITH seeded_account_tags(account_id, tag_key, tag_value) AS (
	VALUES
		('999988887777', 'owner', 'finance-operations'),
		('999988887777', 'cost-center', '1000-corporate'),
		('999988887777', 'product', 'shared-services'),
		('999988887777', 'environment', 'production'),
		('999988887777', 'lifecycle', 'active'),
		('000011112222', 'owner', 'security-platform'),
		('000011112222', 'cost-center', '2100-security'),
		('000011112222', 'product', 'security'),
		('000011112222', 'environment', 'production'),
		('000011112222', 'lifecycle', 'active'),
		('000011112223', 'owner', 'security-platform'),
		('000011112223', 'cost-center', '2100-security'),
		('000011112223', 'product', 'security'),
		('000011112223', 'environment', 'production'),
		('000011112223', 'lifecycle', 'active'),
		('222233334444', 'owner', 'network-platform'),
		('222233334444', 'cost-center', '2200-platform'),
		('222233334444', 'product', 'shared-networking'),
		('222233334444', 'environment', 'production'),
		('222233334444', 'lifecycle', 'active'),
		('222233334445', 'owner', 'platform-engineering'),
		('222233334445', 'cost-center', '2200-platform'),
		('222233334445', 'product', 'platform-services'),
		('222233334445', 'environment', 'production'),
		('222233334445', 'lifecycle', 'active'),
		('333344445555', 'owner', 'developer-enablement'),
		('333344445555', 'cost-center', '3300-sandbox'),
		('333344445555', 'product', 'sandbox'),
		('333344445555', 'environment', 'sandbox'),
		('333344445555', 'lifecycle', 'active'),
		('333344445556', 'owner', 'developer-enablement'),
		('333344445556', 'cost-center', '3300-sandbox'),
		('333344445556', 'product', 'sandbox'),
		('333344445556', 'environment', 'sandbox'),
		('333344445556', 'lifecycle', 'active'),
		('111122223332', 'owner', 'storefront-team'),
		('111122223332', 'cost-center', '4100-storefront'),
		('111122223332', 'product', 'storefront'),
		('111122223332', 'environment', 'development'),
		('111122223332', 'lifecycle', 'active'),
		('111122223333', 'owner', 'storefront-team'),
		('111122223333', 'cost-center', '4100-storefront'),
		('111122223333', 'product', 'storefront'),
		('111122223333', 'environment', 'production'),
		('111122223333', 'lifecycle', 'active'),
		('444455556665', 'owner', 'payments-team'),
		('444455556665', 'cost-center', '4200-payments'),
		('444455556665', 'product', 'payments'),
		('444455556665', 'environment', 'development'),
		('444455556665', 'lifecycle', 'active'),
		('444455556666', 'owner', 'payments-team'),
		('444455556666', 'cost-center', '4200-payments'),
		('444455556666', 'product', 'payments'),
		('444455556666', 'environment', 'production'),
		('444455556666', 'lifecycle', 'active'),
		('555566667777', 'owner', 'data-platform'),
		('555566667777', 'cost-center', '4300-analytics'),
		('555566667777', 'product', 'analytics'),
		('555566667777', 'environment', 'production'),
		('555566667777', 'lifecycle', 'active'),
		('666677778888', 'owner', 'innovation-lab'),
		('666677778888', 'cost-center', '9900-deprecated'),
		('666677778888', 'product', 'prototype'),
		('666677778888', 'environment', 'retired'),
		('666677778888', 'lifecycle', 'deprecated')
)
INSERT INTO account_tags (id, account_id, tag_key, tag_value, applied_at, event_source)
SELECT
	'acct_tag_' || account_id || '_' || replace(tag_key, '-', '_'),
	account_id,
	tag_key,
	tag_value,
	'2026-01-01T00:00:00Z',
	'system'
FROM seeded_account_tags;

PRAGMA user_version = 21;
