CREATE TABLE cost_category_split_charge_rules (
	id TEXT PRIMARY KEY,
	cost_category_id TEXT NOT NULL REFERENCES cost_categories(id) ON UPDATE CASCADE ON DELETE CASCADE,
	source_value TEXT NOT NULL,
	method TEXT NOT NULL CHECK (method IN ('even', 'fixed', 'proportional')),
	description TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(source_value) <> ''),
	UNIQUE (cost_category_id, source_value)
);

CREATE INDEX idx_cost_category_split_rules_category
ON cost_category_split_charge_rules (cost_category_id, status, method);

CREATE TABLE cost_category_split_charge_targets (
	id TEXT PRIMARY KEY,
	rule_id TEXT NOT NULL REFERENCES cost_category_split_charge_rules(id) ON UPDATE CASCADE ON DELETE CASCADE,
	target_order INTEGER NOT NULL CHECK (target_order > 0),
	target_value TEXT NOT NULL,
	fixed_share_micros INTEGER NOT NULL DEFAULT 0 CHECK (fixed_share_micros >= 0),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(target_value) <> ''),
	UNIQUE (rule_id, target_order),
	UNIQUE (rule_id, target_value)
);

CREATE INDEX idx_cost_category_split_targets_rule_order
ON cost_category_split_charge_targets (rule_id, target_order);

CREATE TABLE cost_category_split_charge_allocations (
	rule_id TEXT NOT NULL REFERENCES cost_category_split_charge_rules(id) ON UPDATE CASCADE ON DELETE CASCADE,
	source_line_item_id TEXT NOT NULL REFERENCES bill_line_items(id) ON UPDATE CASCADE ON DELETE CASCADE,
	cost_category_id TEXT NOT NULL REFERENCES cost_categories(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	payer_account_id TEXT NOT NULL,
	usage_account_id TEXT NOT NULL,
	source_value TEXT NOT NULL,
	target_value TEXT NOT NULL,
	method TEXT NOT NULL CHECK (method IN ('even', 'fixed', 'proportional')),
	target_order INTEGER NOT NULL CHECK (target_order > 0),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	source_cost_micros INTEGER NOT NULL CHECK (source_cost_micros >= 0),
	allocation_base_cost_micros INTEGER NOT NULL DEFAULT 0 CHECK (allocation_base_cost_micros >= 0),
	fixed_share_micros INTEGER NOT NULL DEFAULT 0 CHECK (fixed_share_micros >= 0),
	allocated_cost_micros INTEGER NOT NULL CHECK (allocated_cost_micros >= 0),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (rule_id, source_line_item_id, target_value),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(payer_account_id) <> ''),
	CHECK (trim(usage_account_id) <> ''),
	CHECK (trim(source_value) <> ''),
	CHECK (trim(target_value) <> '')
);

CREATE INDEX idx_cost_category_split_allocations_period_target
ON cost_category_split_charge_allocations (
	cost_category_id,
	billing_period_start,
	billing_period_end,
	target_value
);

CREATE INDEX idx_cost_category_split_allocations_period_payer
ON cost_category_split_charge_allocations (
	billing_period_start,
	billing_period_end,
	payer_account_id,
	rule_id
);

CREATE TRIGGER reject_closed_cost_category_split_allocation_insert
BEFORE INSERT ON cost_category_split_charge_allocations
WHEN EXISTS (
	SELECT 1
	FROM billing_period_closes c
	WHERE c.billing_period_start = NEW.billing_period_start
	  AND c.billing_period_end = NEW.billing_period_end
	  AND c.payer_account_id = NEW.payer_account_id
	  AND c.status = 'closed'
)
BEGIN
	SELECT RAISE(ABORT, 'cannot insert cost category split allocations for a closed billing period');
END;

CREATE TRIGGER reject_closed_cost_category_split_allocation_update
BEFORE UPDATE ON cost_category_split_charge_allocations
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
	SELECT RAISE(ABORT, 'cannot update cost category split allocations for a closed billing period');
END;

CREATE TRIGGER reject_closed_cost_category_split_allocation_delete
BEFORE DELETE ON cost_category_split_charge_allocations
WHEN EXISTS (
	SELECT 1
	FROM billing_period_closes c
	WHERE c.billing_period_start = OLD.billing_period_start
	  AND c.billing_period_end = OLD.billing_period_end
	  AND c.payer_account_id = OLD.payer_account_id
	  AND c.status = 'closed'
)
BEGIN
	SELECT RAISE(ABORT, 'cannot delete cost category split allocations for a closed billing period');
END;

PRAGMA user_version = 24;
