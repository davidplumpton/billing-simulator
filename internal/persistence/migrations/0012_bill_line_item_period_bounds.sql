CREATE TRIGGER reject_cross_period_bill_line_item_insert
BEFORE INSERT ON bill_line_items
WHEN NEW.usage_start_time < (NEW.billing_period_start || 'T00:00:00Z')
  OR NEW.usage_end_time > (NEW.billing_period_end || 'T00:00:00Z')
BEGIN
	SELECT RAISE(ABORT, 'bill line item crosses billing period');
END;

CREATE TRIGGER reject_cross_period_bill_line_item_update
BEFORE UPDATE ON bill_line_items
WHEN NEW.usage_start_time < (NEW.billing_period_start || 'T00:00:00Z')
  OR NEW.usage_end_time > (NEW.billing_period_end || 'T00:00:00Z')
BEGIN
	SELECT RAISE(ABORT, 'bill line item crosses billing period');
END;

PRAGMA user_version = 12;
