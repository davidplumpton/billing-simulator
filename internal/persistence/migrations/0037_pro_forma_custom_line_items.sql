CREATE TABLE pro_forma_custom_line_items (
	id TEXT PRIMARY KEY,
	billing_group_id TEXT NOT NULL REFERENCES pro_forma_billing_groups(id) ON UPDATE CASCADE ON DELETE CASCADE,
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	line_item_type TEXT NOT NULL CHECK (line_item_type IN ('fee', 'credit', 'markup', 'annotation')),
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	currency_code TEXT NOT NULL DEFAULT 'USD' CHECK (length(currency_code) = 3),
	amount_micros INTEGER NOT NULL,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(name) <> ''),
	CHECK (
		(line_item_type IN ('fee', 'markup') AND amount_micros > 0)
		OR (line_item_type = 'credit' AND amount_micros < 0)
		OR (line_item_type = 'annotation' AND amount_micros = 0)
	)
);

CREATE INDEX idx_pro_forma_custom_line_items_period_group
ON pro_forma_custom_line_items (billing_period_start, billing_period_end, billing_group_id);

PRAGMA user_version = 37;
