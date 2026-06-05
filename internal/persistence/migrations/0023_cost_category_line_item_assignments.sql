CREATE TABLE cost_category_line_item_assignments (
	line_item_id TEXT NOT NULL REFERENCES bill_line_items(id) ON UPDATE CASCADE ON DELETE CASCADE,
	cost_category_id TEXT NOT NULL REFERENCES cost_categories(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	payer_account_id TEXT NOT NULL,
	usage_account_id TEXT NOT NULL,
	line_item_status TEXT NOT NULL CHECK (line_item_status IN ('estimated', 'final')),
	cost_category_name TEXT NOT NULL,
	category_default_value TEXT NOT NULL,
	assigned_value TEXT NOT NULL,
	assignment_source TEXT NOT NULL CHECK (assignment_source IN ('rule', 'default')),
	matched_rule_id TEXT,
	matched_rule_order INTEGER,
	matched_rule_value TEXT,
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	unblended_cost_micros INTEGER NOT NULL CHECK (unblended_cost_micros >= 0),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	PRIMARY KEY (line_item_id, cost_category_id),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(payer_account_id) <> ''),
	CHECK (trim(usage_account_id) <> ''),
	CHECK (trim(cost_category_name) <> ''),
	CHECK (trim(category_default_value) <> ''),
	CHECK (trim(assigned_value) <> ''),
	CHECK (
		(assignment_source = 'rule' AND matched_rule_id IS NOT NULL AND matched_rule_order IS NOT NULL AND matched_rule_value IS NOT NULL)
		OR
		(assignment_source = 'default' AND matched_rule_id IS NULL AND matched_rule_order IS NULL AND matched_rule_value IS NULL)
	)
);

CREATE INDEX idx_cost_category_assignments_period_value
ON cost_category_line_item_assignments (
	cost_category_id,
	billing_period_start,
	billing_period_end,
	assigned_value
);

CREATE INDEX idx_cost_category_assignments_period_payer
ON cost_category_line_item_assignments (
	billing_period_start,
	billing_period_end,
	payer_account_id,
	cost_category_id
);

CREATE TRIGGER reject_closed_cost_category_assignment_insert
BEFORE INSERT ON cost_category_line_item_assignments
WHEN EXISTS (
	SELECT 1
	FROM billing_period_closes c
	WHERE c.billing_period_start = NEW.billing_period_start
	  AND c.billing_period_end = NEW.billing_period_end
	  AND c.payer_account_id = NEW.payer_account_id
	  AND c.status = 'closed'
)
BEGIN
	SELECT RAISE(ABORT, 'cannot insert cost category assignments for a closed billing period');
END;

CREATE TRIGGER reject_closed_cost_category_assignment_update
BEFORE UPDATE ON cost_category_line_item_assignments
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
	SELECT RAISE(ABORT, 'cannot update cost category assignments for a closed billing period');
END;

CREATE TRIGGER reject_closed_cost_category_assignment_delete
BEFORE DELETE ON cost_category_line_item_assignments
WHEN EXISTS (
	SELECT 1
	FROM billing_period_closes c
	WHERE c.billing_period_start = OLD.billing_period_start
	  AND c.billing_period_end = OLD.billing_period_end
	  AND c.payer_account_id = OLD.payer_account_id
	  AND c.status = 'closed'
)
BEGIN
	SELECT RAISE(ABORT, 'cannot delete cost category assignments for a closed billing period');
END;

PRAGMA user_version = 23;
