CREATE TABLE billing_period_closes (
	id TEXT PRIMARY KEY,
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	payer_account_id TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('closed')),
	metering_records_created INTEGER NOT NULL CHECK (metering_records_created >= 0),
	bill_line_items_created INTEGER NOT NULL CHECK (bill_line_items_created >= 0),
	finalized_line_item_count INTEGER NOT NULL CHECK (finalized_line_item_count >= 0),
	finalized_cost_micros INTEGER NOT NULL CHECK (finalized_cost_micros >= 0),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	summaries_refreshed INTEGER NOT NULL CHECK (summaries_refreshed >= 0),
	closed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(id) <> ''),
	CHECK (trim(payer_account_id) <> ''),
	UNIQUE (billing_period_start, billing_period_end, payer_account_id)
);

CREATE INDEX idx_billing_period_closes_closed
ON billing_period_closes (closed_at DESC, id DESC);

CREATE TABLE bills (
	id TEXT PRIMARY KEY,
	close_id TEXT NOT NULL UNIQUE REFERENCES billing_period_closes(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	billing_period_start TEXT NOT NULL,
	billing_period_end TEXT NOT NULL,
	payer_account_id TEXT NOT NULL,
	bill_state TEXT NOT NULL CHECK (bill_state IN ('issued', 'adjusted', 'paid', 'past_due')),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	line_item_count INTEGER NOT NULL CHECK (line_item_count >= 0),
	usage_charge_micros INTEGER NOT NULL CHECK (usage_charge_micros >= 0),
	credit_micros INTEGER NOT NULL CHECK (credit_micros >= 0),
	refund_micros INTEGER NOT NULL CHECK (refund_micros >= 0),
	tax_micros INTEGER NOT NULL CHECK (tax_micros >= 0),
	total_micros INTEGER NOT NULL CHECK (total_micros >= 0),
	issued_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (billing_period_start < billing_period_end),
	CHECK (trim(id) <> ''),
	CHECK (trim(payer_account_id) <> ''),
	UNIQUE (billing_period_start, billing_period_end, payer_account_id, currency_code)
);

CREATE INDEX idx_bills_period_payer
ON bills (billing_period_start, billing_period_end, payer_account_id);

CREATE TABLE invoice_obligations (
	id TEXT PRIMARY KEY,
	bill_id TEXT NOT NULL UNIQUE REFERENCES bills(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	invoice_id TEXT NOT NULL UNIQUE,
	status TEXT NOT NULL CHECK (status IN ('due', 'scheduled', 'processing', 'paid', 'past_due', 'failed')),
	amount_due_micros INTEGER NOT NULL CHECK (amount_due_micros >= 0),
	amount_paid_micros INTEGER NOT NULL DEFAULT 0 CHECK (amount_paid_micros >= 0),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	invoice_date TEXT NOT NULL,
	due_date TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(invoice_id) <> ''),
	CHECK (invoice_date <= due_date)
);

CREATE INDEX idx_invoice_obligations_due
ON invoice_obligations (due_date, status);

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

PRAGMA user_version = 11;
