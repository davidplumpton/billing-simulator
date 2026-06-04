CREATE TABLE organizations (
	id TEXT PRIMARY KEY,
	template_key TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL,
	management_account_id TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(template_key) <> ''),
	CHECK (trim(name) <> ''),
	CHECK (trim(management_account_id) <> '')
);

CREATE TABLE organization_units (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL REFERENCES organizations(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	parent_unit_id TEXT REFERENCES organization_units(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	name TEXT NOT NULL,
	path TEXT NOT NULL,
	sort_order INTEGER NOT NULL CHECK (sort_order >= 0),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(organization_id) <> ''),
	CHECK (trim(name) <> ''),
	CHECK (trim(path) <> ''),
	UNIQUE (organization_id, parent_unit_id, name),
	UNIQUE (organization_id, path)
);

CREATE INDEX idx_organization_units_parent
ON organization_units (organization_id, parent_unit_id, sort_order);

CREATE TABLE accounts (
	id TEXT PRIMARY KEY,
	organization_id TEXT NOT NULL REFERENCES organizations(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	parent_unit_id TEXT NOT NULL REFERENCES organization_units(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	name TEXT NOT NULL,
	email TEXT NOT NULL UNIQUE,
	account_type TEXT NOT NULL CHECK (account_type IN ('management', 'member')),
	status TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'closed')),
	created_at TEXT NOT NULL,
	joined_at TEXT NOT NULL,
	left_at TEXT,
	payment_responsibility TEXT NOT NULL CHECK (payment_responsibility IN ('management_account', 'standalone', 'transferred')),
	payer_account_id TEXT NOT NULL,
	billing_visibility_role TEXT NOT NULL CHECK (billing_visibility_role IN ('management-account', 'member-account')),
	sort_order INTEGER NOT NULL CHECK (sort_order >= 0),
	CHECK (trim(id) <> ''),
	CHECK (trim(organization_id) <> ''),
	CHECK (trim(parent_unit_id) <> ''),
	CHECK (trim(name) <> ''),
	CHECK (trim(email) <> ''),
	CHECK (trim(created_at) <> ''),
	CHECK (trim(joined_at) <> ''),
	CHECK (left_at IS NULL OR left_at > joined_at),
	CHECK (trim(payer_account_id) <> ''),
	CHECK (
		(account_type = 'management' AND payer_account_id = id AND billing_visibility_role = 'management-account')
		OR
		(account_type = 'member' AND billing_visibility_role = 'member-account')
	),
	UNIQUE (organization_id, name)
);

CREATE UNIQUE INDEX idx_accounts_one_management_per_org
ON accounts (organization_id)
WHERE account_type = 'management';

CREATE INDEX idx_accounts_parent_unit
ON accounts (organization_id, parent_unit_id, sort_order);

CREATE INDEX idx_accounts_payer_status
ON accounts (payer_account_id, status);

INSERT INTO organizations (id, template_key, name, management_account_id, created_at)
VALUES ('org_anycompany_retail', 'anycompany-retail', 'AnyCompany Retail', '999988887777', '2026-01-01T00:00:00Z');

INSERT INTO organization_units (id, organization_id, parent_unit_id, name, path, sort_order, created_at)
VALUES
	('ou_anycompany_root', 'org_anycompany_retail', NULL, 'Root', 'Root', 0, '2026-01-01T00:00:00Z'),
	('ou_anycompany_security', 'org_anycompany_retail', 'ou_anycompany_root', 'Security', 'Root/Security', 10, '2026-01-01T00:00:00Z'),
	('ou_anycompany_infrastructure', 'org_anycompany_retail', 'ou_anycompany_root', 'Infrastructure', 'Root/Infrastructure', 20, '2026-01-01T00:00:00Z'),
	('ou_anycompany_sandbox', 'org_anycompany_retail', 'ou_anycompany_root', 'Sandbox', 'Root/Sandbox', 30, '2026-01-01T00:00:00Z'),
	('ou_anycompany_workloads', 'org_anycompany_retail', 'ou_anycompany_root', 'Workloads', 'Root/Workloads', 40, '2026-01-01T00:00:00Z'),
	('ou_anycompany_suspended', 'org_anycompany_retail', 'ou_anycompany_root', 'Suspended', 'Root/Suspended', 50, '2026-01-01T00:00:00Z');

INSERT INTO accounts (
	id,
	organization_id,
	parent_unit_id,
	name,
	email,
	account_type,
	status,
	created_at,
	joined_at,
	left_at,
	payment_responsibility,
	payer_account_id,
	billing_visibility_role,
	sort_order
)
VALUES
	('999988887777', 'org_anycompany_retail', 'ou_anycompany_root', 'Management', 'management@anycompany.example', 'management', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL, 'management_account', '999988887777', 'management-account', 0),
	('000011112222', 'org_anycompany_retail', 'ou_anycompany_security', 'Log Archive', 'log-archive@anycompany.example', 'member', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL, 'management_account', '999988887777', 'member-account', 10),
	('000011112223', 'org_anycompany_retail', 'ou_anycompany_security', 'Audit', 'audit@anycompany.example', 'member', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL, 'management_account', '999988887777', 'member-account', 20),
	('222233334444', 'org_anycompany_retail', 'ou_anycompany_infrastructure', 'Shared Networking', 'shared-networking@anycompany.example', 'member', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL, 'management_account', '999988887777', 'member-account', 30),
	('222233334445', 'org_anycompany_retail', 'ou_anycompany_infrastructure', 'Platform Services', 'platform-services@anycompany.example', 'member', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL, 'management_account', '999988887777', 'member-account', 40),
	('333344445555', 'org_anycompany_retail', 'ou_anycompany_sandbox', 'Developer Sandbox 1', 'developer-sandbox-1@anycompany.example', 'member', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL, 'management_account', '999988887777', 'member-account', 50),
	('333344445556', 'org_anycompany_retail', 'ou_anycompany_sandbox', 'Developer Sandbox 2', 'developer-sandbox-2@anycompany.example', 'member', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL, 'management_account', '999988887777', 'member-account', 60),
	('111122223332', 'org_anycompany_retail', 'ou_anycompany_workloads', 'Storefront Dev', 'storefront-dev@anycompany.example', 'member', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL, 'management_account', '999988887777', 'member-account', 70),
	('111122223333', 'org_anycompany_retail', 'ou_anycompany_workloads', 'Storefront Prod', 'storefront-prod@anycompany.example', 'member', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL, 'management_account', '999988887777', 'member-account', 80),
	('444455556665', 'org_anycompany_retail', 'ou_anycompany_workloads', 'Payments Dev', 'payments-dev@anycompany.example', 'member', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL, 'management_account', '999988887777', 'member-account', 90),
	('444455556666', 'org_anycompany_retail', 'ou_anycompany_workloads', 'Payments Prod', 'payments-prod@anycompany.example', 'member', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL, 'management_account', '999988887777', 'member-account', 100),
	('555566667777', 'org_anycompany_retail', 'ou_anycompany_workloads', 'Analytics Prod', 'analytics-prod@anycompany.example', 'member', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL, 'management_account', '999988887777', 'member-account', 110),
	('666677778888', 'org_anycompany_retail', 'ou_anycompany_suspended', 'Deprecated Prototype', 'deprecated-prototype@anycompany.example', 'member', 'suspended', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL, 'management_account', '999988887777', 'member-account', 120);

PRAGMA user_version = 16;
