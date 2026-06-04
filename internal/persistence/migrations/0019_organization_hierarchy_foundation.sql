CREATE TABLE organization_roots (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL UNIQUE REFERENCES organizations(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	name TEXT NOT NULL,
	path TEXT NOT NULL,
	sort_order INTEGER NOT NULL CHECK (sort_order >= 0),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(organization_id) <> ''),
	CHECK (trim(name) <> ''),
	CHECK (trim(path) <> ''),
	UNIQUE (organization_id, path)
);

INSERT INTO organization_roots (id, organization_id, name, path, sort_order, created_at)
SELECT id, organization_id, name, path, sort_order, created_at
  FROM organization_units
 WHERE parent_unit_id IS NULL;

ALTER TABLE accounts
ADD COLUMN is_management_account INTEGER NOT NULL DEFAULT 0 CHECK (is_management_account IN (0, 1));

UPDATE accounts
   SET is_management_account = CASE WHEN account_type = 'management' THEN 1 ELSE 0 END;

CREATE UNIQUE INDEX idx_accounts_one_management_flag_per_org
ON accounts (organization_id)
WHERE is_management_account = 1;

CREATE INDEX idx_organization_roots_org_order
ON organization_roots (organization_id, sort_order);

CREATE TRIGGER accounts_management_flag_insert
BEFORE INSERT ON accounts
WHEN
	(NEW.account_type = 'management' AND NEW.is_management_account <> 1)
	OR
	(NEW.account_type <> 'management' AND NEW.is_management_account <> 0)
BEGIN
	SELECT RAISE(ABORT, 'account_type and is_management_account must agree');
END;

CREATE TRIGGER accounts_management_flag_update
BEFORE UPDATE OF account_type, is_management_account ON accounts
WHEN
	(NEW.account_type = 'management' AND NEW.is_management_account <> 1)
	OR
	(NEW.account_type <> 'management' AND NEW.is_management_account <> 0)
BEGIN
	SELECT RAISE(ABORT, 'account_type and is_management_account must agree');
END;

CREATE VIEW organization_account_hierarchy AS
SELECT
	a.id,
	a.organization_id,
	a.parent_unit_id,
	u.path AS ou_path,
	a.name,
	a.email,
	a.account_type,
	a.status,
	a.created_at,
	a.joined_at,
	a.left_at,
	a.payment_responsibility,
	a.payer_account_id,
	a.billing_visibility_role,
	a.is_management_account,
	a.sort_order
FROM accounts a
JOIN organization_units u ON u.id = a.parent_unit_id;

PRAGMA user_version = 19;
