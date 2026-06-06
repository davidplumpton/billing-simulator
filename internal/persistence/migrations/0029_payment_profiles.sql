CREATE TABLE payment_seller_profiles (
	id TEXT PRIMARY KEY,
	seller_of_record TEXT NOT NULL,
	seller_address TEXT NOT NULL,
	seller_tax_registration TEXT NOT NULL DEFAULT '',
	remittance_instructions TEXT NOT NULL DEFAULT '',
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	status TEXT NOT NULL CHECK (status IN ('active', 'inactive')),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(seller_of_record) <> ''),
	CHECK (trim(seller_address) <> '')
);

CREATE TABLE payment_profiles (
	id TEXT PRIMARY KEY,
	payer_account_id TEXT NOT NULL,
	seller_profile_id TEXT NOT NULL REFERENCES payment_seller_profiles(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	profile_name TEXT NOT NULL,
	bill_to_name TEXT NOT NULL,
	bill_to_email TEXT NOT NULL,
	bill_to_address TEXT NOT NULL,
	bill_to_tax_registration TEXT NOT NULL DEFAULT '',
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	status TEXT NOT NULL CHECK (status IN ('active', 'inactive')),
	is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(payer_account_id) <> ''),
	CHECK (trim(seller_profile_id) <> ''),
	CHECK (trim(profile_name) <> ''),
	CHECK (trim(bill_to_name) <> ''),
	CHECK (trim(bill_to_email) <> ''),
	CHECK (trim(bill_to_address) <> ''),
	UNIQUE (payer_account_id, profile_name)
);

CREATE UNIQUE INDEX idx_payment_profiles_default_payer_currency
ON payment_profiles (payer_account_id, currency_code)
WHERE is_default = 1;

CREATE INDEX idx_payment_profiles_payer_status
ON payment_profiles (payer_account_id, status, currency_code);

CREATE TRIGGER reject_default_inactive_payment_profile_insert
BEFORE INSERT ON payment_profiles
WHEN NEW.is_default = 1 AND NEW.status <> 'active'
BEGIN
	SELECT RAISE(ABORT, 'default payment profile must be active');
END;

CREATE TRIGGER reject_default_inactive_payment_profile_update
BEFORE UPDATE ON payment_profiles
WHEN NEW.is_default = 1 AND NEW.status <> 'active'
BEGIN
	SELECT RAISE(ABORT, 'default payment profile must be active');
END;

CREATE TABLE payment_methods (
	id TEXT PRIMARY KEY,
	payment_profile_id TEXT NOT NULL REFERENCES payment_profiles(id) ON UPDATE CASCADE ON DELETE CASCADE,
	method_type TEXT NOT NULL CHECK (method_type IN ('card', 'ach', 'invoice_remittance', 'advance_pay_balance')),
	display_name TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('active', 'inactive', 'failed', 'expired')),
	is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
	currency_code TEXT NOT NULL CHECK (length(currency_code) = 3),
	card_brand TEXT NOT NULL DEFAULT '',
	account_last4 TEXT NOT NULL DEFAULT '',
	expiration_month INTEGER CHECK (expiration_month IS NULL OR expiration_month BETWEEN 1 AND 12),
	expiration_year INTEGER CHECK (expiration_year IS NULL OR expiration_year >= 2000),
	bank_name TEXT NOT NULL DEFAULT '',
	remittance_destination TEXT NOT NULL DEFAULT '',
	advance_pay_balance_micros INTEGER NOT NULL DEFAULT 0 CHECK (advance_pay_balance_micros >= 0),
	failure_reason TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(payment_profile_id) <> ''),
	CHECK (trim(display_name) <> ''),
	CHECK (account_last4 = '' OR account_last4 GLOB '[0-9][0-9][0-9][0-9]'),
	CHECK (method_type <> 'card' OR (trim(card_brand) <> '' AND trim(account_last4) <> '' AND expiration_month IS NOT NULL AND expiration_year IS NOT NULL)),
	CHECK (method_type <> 'ach' OR (trim(bank_name) <> '' AND trim(account_last4) <> '')),
	CHECK (method_type <> 'invoice_remittance' OR trim(remittance_destination) <> '')
);

CREATE UNIQUE INDEX idx_payment_methods_default_profile
ON payment_methods (payment_profile_id)
WHERE is_default = 1;

CREATE INDEX idx_payment_methods_profile_status
ON payment_methods (payment_profile_id, status, method_type);

CREATE TRIGGER reject_default_inactive_payment_method_insert
BEFORE INSERT ON payment_methods
WHEN NEW.is_default = 1 AND NEW.status <> 'active'
BEGIN
	SELECT RAISE(ABORT, 'default payment method must be active');
END;

CREATE TRIGGER reject_default_inactive_payment_method_update
BEFORE UPDATE ON payment_methods
WHEN NEW.is_default = 1 AND NEW.status <> 'active'
BEGIN
	SELECT RAISE(ABORT, 'default payment method must be active');
END;

INSERT INTO payment_seller_profiles (
	id,
	seller_of_record,
	seller_address,
	seller_tax_registration,
	remittance_instructions,
	currency_code,
	status
)
VALUES (
	'seller_aws_billing_simulator',
	'AWS Billing Simulator',
	'Local synthetic invoice lab',
	'',
	'Synthetic invoice remittance only; no real payment leaves the simulator.',
	'USD',
	'active'
);

INSERT INTO payment_profiles (
	id,
	payer_account_id,
	seller_profile_id,
	profile_name,
	bill_to_name,
	bill_to_email,
	bill_to_address,
	bill_to_tax_registration,
	currency_code,
	status,
	is_default
)
VALUES (
	'payprof_anycompany_retail_management',
	'999988887777',
	'seller_aws_billing_simulator',
	'AnyCompany Retail default',
	'AnyCompany Retail',
	'billing@anycompany.example',
	'100 AnyCompany Way, Example City',
	'',
	'USD',
	'active',
	1
);

INSERT INTO payment_methods (
	id,
	payment_profile_id,
	method_type,
	display_name,
	status,
	is_default,
	currency_code,
	remittance_destination
)
VALUES (
	'paymeth_anycompany_invoice_remittance',
	'payprof_anycompany_retail_management',
	'invoice_remittance',
	'Invoice remittance',
	'active',
	1,
	'USD',
	'Synthetic invoice remittance only; no real payment leaves the simulator.'
);

PRAGMA user_version = 29;
