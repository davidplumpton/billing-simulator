CREATE TABLE invoice_payment_states (
	invoice_obligation_id TEXT PRIMARY KEY REFERENCES invoice_obligations(id) ON UPDATE CASCADE ON DELETE CASCADE,
	status TEXT NOT NULL CHECK (status IN ('due', 'scheduled', 'processing', 'succeeded', 'failed', 'past_due', 'partially_paid', 'refunded')),
	amount_due_micros INTEGER NOT NULL CHECK (amount_due_micros >= 0),
	amount_paid_micros INTEGER NOT NULL DEFAULT 0 CHECK (amount_paid_micros >= 0),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	last_failure_reason TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(invoice_obligation_id) <> '')
);

CREATE INDEX idx_invoice_payment_states_status
ON invoice_payment_states (status, updated_at DESC);

CREATE TABLE invoice_payment_events (
	id TEXT PRIMARY KEY,
	invoice_obligation_id TEXT NOT NULL REFERENCES invoice_obligations(id) ON UPDATE CASCADE ON DELETE CASCADE,
	transition_kind TEXT NOT NULL CHECK (transition_kind IN ('created', 'due', 'scheduled', 'processing', 'succeeded', 'failed', 'past_due', 'partially_paid', 'refunded')),
	from_status TEXT NOT NULL DEFAULT '' CHECK (from_status = '' OR from_status IN ('due', 'scheduled', 'processing', 'succeeded', 'failed', 'past_due', 'partially_paid', 'refunded')),
	to_status TEXT NOT NULL CHECK (to_status IN ('due', 'scheduled', 'processing', 'succeeded', 'failed', 'past_due', 'partially_paid', 'refunded')),
	amount_micros INTEGER NOT NULL DEFAULT 0 CHECK (amount_micros >= 0),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	reason TEXT NOT NULL DEFAULT '',
	occurred_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(invoice_obligation_id) <> '')
);

CREATE INDEX idx_invoice_payment_events_obligation
ON invoice_payment_events (invoice_obligation_id, occurred_at DESC, created_at DESC);

INSERT INTO invoice_payment_states (
	invoice_obligation_id,
	status,
	amount_due_micros,
	amount_paid_micros,
	currency_code,
	last_failure_reason
)
SELECT
	id,
	CASE status
		WHEN 'paid' THEN 'succeeded'
		ELSE status
	END,
	amount_due_micros,
	amount_paid_micros,
	currency_code,
	''
FROM invoice_obligations;

INSERT INTO invoice_payment_events (
	id,
	invoice_obligation_id,
	transition_kind,
	from_status,
	to_status,
	amount_micros,
	currency_code,
	reason,
	occurred_at
)
SELECT
	'payevt_initial_' || id,
	id,
	'created',
	'',
	CASE status
		WHEN 'paid' THEN 'succeeded'
		ELSE status
	END,
	amount_paid_micros,
	currency_code,
	'Initial invoice obligation state',
	invoice_date
FROM invoice_obligations;

CREATE TRIGGER seed_invoice_payment_state_after_obligation_insert
AFTER INSERT ON invoice_obligations
WHEN NOT EXISTS (
	SELECT 1
	FROM invoice_payment_states
	WHERE invoice_obligation_id = NEW.id
)
BEGIN
	INSERT INTO invoice_payment_states (
		invoice_obligation_id,
		status,
		amount_due_micros,
		amount_paid_micros,
		currency_code,
		last_failure_reason
	) VALUES (
		NEW.id,
		CASE NEW.status
			WHEN 'paid' THEN 'succeeded'
			ELSE NEW.status
		END,
		NEW.amount_due_micros,
		NEW.amount_paid_micros,
		NEW.currency_code,
		''
	);

	INSERT INTO invoice_payment_events (
		id,
		invoice_obligation_id,
		transition_kind,
		from_status,
		to_status,
		amount_micros,
		currency_code,
		reason,
		occurred_at
	) VALUES (
		'payevt_initial_' || NEW.id,
		NEW.id,
		'created',
		'',
		CASE NEW.status
			WHEN 'paid' THEN 'succeeded'
			ELSE NEW.status
		END,
		NEW.amount_paid_micros,
		NEW.currency_code,
		'Initial invoice obligation state',
		NEW.invoice_date
	);
END;

PRAGMA user_version = 30;
