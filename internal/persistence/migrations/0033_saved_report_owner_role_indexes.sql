DROP INDEX IF EXISTS idx_saved_reports_owner_name;
DROP INDEX IF EXISTS idx_saved_reports_owner_updated;

CREATE UNIQUE INDEX idx_saved_reports_owner_name
ON saved_reports (owner_account_id, owner_role, lower(name));

CREATE INDEX idx_saved_reports_owner_updated
ON saved_reports (owner_account_id, owner_role, updated_at DESC, id DESC);

PRAGMA user_version = 33;
