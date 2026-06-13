ALTER TABLE saved_reports
ADD COLUMN last_run_metric TEXT NOT NULL DEFAULT 'unblended_cost'
CHECK (last_run_metric IN ('unblended_cost', 'blended_cost', 'net_cost', 'amortized_cost', 'usage_quantity'));

ALTER TABLE saved_reports
ADD COLUMN last_run_metric_total_micros INTEGER NOT NULL DEFAULT 0;

PRAGMA user_version = 40;
