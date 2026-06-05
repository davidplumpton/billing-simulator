CREATE TABLE cost_categories (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	default_value TEXT NOT NULL DEFAULT 'Uncategorized',
	status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(name) <> ''),
	CHECK (trim(default_value) <> '')
);

CREATE UNIQUE INDEX idx_cost_categories_name
ON cost_categories (lower(name));

CREATE INDEX idx_cost_categories_status_name
ON cost_categories (status, lower(name));

CREATE TABLE cost_category_rules (
	id TEXT PRIMARY KEY,
	cost_category_id TEXT NOT NULL REFERENCES cost_categories(id) ON UPDATE CASCADE ON DELETE CASCADE,
	rule_order INTEGER NOT NULL CHECK (rule_order > 0),
	value TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	match_type TEXT NOT NULL DEFAULT 'all' CHECK (match_type IN ('all')),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (trim(value) <> ''),
	UNIQUE (cost_category_id, rule_order)
);

CREATE INDEX idx_cost_category_rules_category_order
ON cost_category_rules (cost_category_id, rule_order);

CREATE TABLE cost_category_rule_conditions (
	id TEXT PRIMARY KEY,
	rule_id TEXT NOT NULL REFERENCES cost_category_rules(id) ON UPDATE CASCADE ON DELETE CASCADE,
	condition_order INTEGER NOT NULL CHECK (condition_order > 0),
	dimension TEXT NOT NULL CHECK (dimension IN ('account', 'service', 'region', 'usage_type', 'line_item_type', 'tag', 'cost_category')),
	operator TEXT NOT NULL DEFAULT 'in' CHECK (operator IN ('in', 'not_in')),
	tag_key TEXT,
	cost_category_id TEXT REFERENCES cost_categories(id) ON UPDATE CASCADE ON DELETE RESTRICT,
	values_json TEXT NOT NULL CHECK (json_valid(values_json) AND json_type(values_json) = 'array' AND json_array_length(values_json) > 0),
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	CHECK (trim(id) <> ''),
	CHECK (dimension = 'tag' OR tag_key IS NULL),
	CHECK (dimension <> 'tag' OR (tag_key IS NOT NULL AND trim(tag_key) <> '')),
	CHECK (dimension = 'cost_category' OR cost_category_id IS NULL),
	CHECK (dimension <> 'cost_category' OR cost_category_id IS NOT NULL),
	UNIQUE (rule_id, condition_order)
);

CREATE INDEX idx_cost_category_rule_conditions_rule_order
ON cost_category_rule_conditions (rule_id, condition_order);

CREATE INDEX idx_cost_category_rule_conditions_dimension
ON cost_category_rule_conditions (dimension, tag_key, cost_category_id);

CREATE VIEW active_cost_category_rules AS
SELECT
	c.id AS cost_category_id,
	c.name AS cost_category_name,
	r.id AS rule_id,
	r.rule_order,
	r.value,
	r.match_type
FROM cost_categories c
JOIN cost_category_rules r ON r.cost_category_id = c.id
WHERE c.status = 'active';

PRAGMA user_version = 22;
