CREATE TABLE pro_forma_pricing_plans (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	description TEXT NOT NULL DEFAULT '',
	currency_code TEXT NOT NULL DEFAULT 'USD' CHECK (length(currency_code) = 3),
	status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(name) <> '')
);

CREATE TABLE pro_forma_pricing_rules (
	id TEXT PRIMARY KEY,
	pricing_plan_id TEXT NOT NULL REFERENCES pro_forma_pricing_plans(id) ON UPDATE CASCADE ON DELETE CASCADE,
	service_code TEXT NOT NULL,
	rate_multiplier_basis_points INTEGER NOT NULL CHECK (rate_multiplier_basis_points > 0 AND rate_multiplier_basis_points <= 1000000),
	description TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	UNIQUE (pricing_plan_id, service_code),
	CHECK (trim(id) <> ''),
	CHECK (trim(service_code) <> '')
);

CREATE INDEX idx_pro_forma_pricing_rules_plan
ON pro_forma_pricing_rules (pricing_plan_id, status, service_code);

CREATE TABLE pro_forma_billing_groups (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	description TEXT NOT NULL DEFAULT '',
	payer_account_id TEXT NOT NULL,
	pricing_plan_id TEXT NOT NULL REFERENCES pro_forma_pricing_plans(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(name) <> ''),
	CHECK (trim(payer_account_id) <> '')
);

CREATE INDEX idx_pro_forma_billing_groups_plan
ON pro_forma_billing_groups (pricing_plan_id, status);

CREATE TABLE pro_forma_billing_group_accounts (
	id TEXT PRIMARY KEY,
	billing_group_id TEXT NOT NULL REFERENCES pro_forma_billing_groups(id) ON UPDATE CASCADE ON DELETE CASCADE,
	account_id TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	UNIQUE (billing_group_id, account_id),
	UNIQUE (account_id),
	CHECK (trim(id) <> ''),
	CHECK (trim(account_id) <> '')
);

CREATE INDEX idx_pro_forma_billing_group_accounts_group
ON pro_forma_billing_group_accounts (billing_group_id, account_id);

CREATE TABLE pro_forma_line_items (
	id TEXT PRIMARY KEY,
	source_bill_line_item_id TEXT NOT NULL REFERENCES bill_line_items(id) ON UPDATE CASCADE ON DELETE CASCADE,
	billing_group_id TEXT NOT NULL REFERENCES pro_forma_billing_groups(id) ON UPDATE CASCADE ON DELETE CASCADE,
	pricing_plan_id TEXT NOT NULL REFERENCES pro_forma_pricing_plans(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	pricing_rule_id TEXT REFERENCES pro_forma_pricing_rules(id) ON UPDATE CASCADE ON DELETE SET NULL,
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	payer_account_id TEXT NOT NULL,
	usage_account_id TEXT NOT NULL,
	service_code TEXT NOT NULL,
	service_name TEXT NOT NULL,
	usage_type TEXT NOT NULL,
	line_item_status TEXT NOT NULL CHECK (line_item_status IN ('estimated', 'final')),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	source_rate_micros INTEGER NOT NULL CHECK (source_rate_micros >= 0),
	source_cost_micros INTEGER NOT NULL CHECK (source_cost_micros >= 0),
	rate_multiplier_basis_points INTEGER NOT NULL CHECK (rate_multiplier_basis_points > 0),
	pro_forma_rate_micros INTEGER NOT NULL CHECK (pro_forma_rate_micros >= 0),
	pro_forma_cost_micros INTEGER NOT NULL CHECK (pro_forma_cost_micros >= 0),
	adjustment_micros INTEGER NOT NULL,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	UNIQUE (source_bill_line_item_id, billing_group_id),
	CHECK (trim(id) <> ''),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(payer_account_id) <> ''),
	CHECK (trim(usage_account_id) <> ''),
	CHECK (trim(service_code) <> ''),
	CHECK (trim(service_name) <> ''),
	CHECK (trim(usage_type) <> '')
);

CREATE INDEX idx_pro_forma_line_items_period_group
ON pro_forma_line_items (billing_period_start, billing_period_end, billing_group_id);

CREATE INDEX idx_pro_forma_line_items_payer_period
ON pro_forma_line_items (payer_account_id, billing_period_start, billing_period_end);

PRAGMA user_version = 36;
