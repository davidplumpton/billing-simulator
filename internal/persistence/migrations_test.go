package persistence

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenWorkspaceCreatesDatabaseAndRecordsMigrations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspacePath := t.TempDir()

	db, err := OpenWorkspace(ctx, workspacePath)
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	assertMigrationState(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, err := os.Stat(WorkspaceDBPath(workspacePath)); err != nil {
		t.Fatalf("workspace database was not created: %v", err)
	}

	db, err = OpenWorkspace(ctx, workspacePath)
	if err != nil {
		t.Fatalf("OpenWorkspace() second run error = %v", err)
	}
	defer db.Close()
	assertMigrationState(t, db)
}

func TestOpenWorkspaceConfiguresSQLiteConcurrencyPragmas(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspacePath := filepath.Join(t.TempDir(), "workspace with spaces")

	db, err := OpenWorkspace(ctx, workspacePath)
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	defer db.Close()

	var journalMode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var foreignKeysEnabled int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeysEnabled); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeysEnabled != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeysEnabled)
	}

	var busyTimeoutMS int
	if err := db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeoutMS); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	wantBusyTimeoutMS := int(workspaceBusyTimeout / time.Millisecond)
	if busyTimeoutMS != wantBusyTimeoutMS {
		t.Fatalf("busy_timeout = %d, want %d", busyTimeoutMS, wantBusyTimeoutMS)
	}
}

func TestApplyMigrationsRejectsChangedHistory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := sql.Open(sqliteDriver, ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE schema_migrations SET checksum = 'changed' WHERE version = 1`); err != nil {
		t.Fatalf("tamper schema_migrations: %v", err)
	}

	err = ApplyMigrations(ctx, db)
	if err == nil {
		t.Fatal("ApplyMigrations() error = nil, want changed migration error")
	}
	if !strings.Contains(err.Error(), "changed since it was applied") {
		t.Fatalf("ApplyMigrations() error = %q, want changed migration message", err.Error())
	}
}

func assertMigrationState(t *testing.T, db *sql.DB) {
	t.Helper()

	ctx := context.Background()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 32 {
		t.Fatalf("schema_migrations count = %d, want 32", count)
	}

	assertMigrationRecorded(t, db, 1, "workspace_metadata")
	assertMigrationRecorded(t, db, 2, "price_catalog_items")
	assertMigrationRecorded(t, db, 3, "price_catalog_rate_versions")
	assertMigrationRecorded(t, db, 4, "price_catalog_lookup_identity")
	assertMigrationRecorded(t, db, 5, "price_catalog_positive_rates")
	assertMigrationRecorded(t, db, 6, "resource_usage_events")
	assertMigrationRecorded(t, db, 7, "metering_records")
	assertMigrationRecorded(t, db, 8, "bill_line_items")
	assertMigrationRecorded(t, db, 9, "simulator_clock")
	assertMigrationRecorded(t, db, 10, "daily_metering_jobs")
	assertMigrationRecorded(t, db, 11, "month_end_close")
	assertMigrationRecorded(t, db, 12, "bill_line_item_period_bounds")
	assertMigrationRecorded(t, db, 13, "period_level_support_charges")
	assertMigrationRecorded(t, db, 14, "invoice_documents")
	assertMigrationRecorded(t, db, 15, "scenario_runs")
	assertMigrationRecorded(t, db, 16, "anycompany_retail_organization")
	assertMigrationRecorded(t, db, 17, "cost_allocation_tags")
	assertMigrationRecorded(t, db, 18, "saved_reports")
	assertMigrationRecorded(t, db, 19, "organization_hierarchy_foundation")
	assertMigrationRecorded(t, db, 20, "account_lifecycle_events")
	assertMigrationRecorded(t, db, 21, "account_tags_seed_metadata")
	assertMigrationRecorded(t, db, 22, "cost_category_rules")
	assertMigrationRecorded(t, db, 23, "cost_category_line_item_assignments")
	assertMigrationRecorded(t, db, 24, "cost_category_split_charges")
	assertMigrationRecorded(t, db, 25, "cost_explorer_summaries")
	assertMigrationRecorded(t, db, 26, "budget_model")
	assertMigrationRecorded(t, db, 27, "budget_forecast_summaries")
	assertMigrationRecorded(t, db, 28, "budget_alert_notifications")
	assertMigrationRecorded(t, db, 29, "payment_profiles")
	assertMigrationRecorded(t, db, 30, "payment_lifecycle")
	assertMigrationRecorded(t, db, 31, "scenario_learner_progress")
	assertMigrationRecorded(t, db, 32, "workspace_export_files")

	var schemaKind string
	if err := db.QueryRowContext(
		ctx,
		`SELECT value FROM workspace_metadata WHERE key = 'schema_kind'`,
	).Scan(&schemaKind); err != nil {
		t.Fatalf("read workspace metadata: %v", err)
	}
	if schemaKind != "aws-billing-simulator" {
		t.Fatalf("schema_kind = %q, want aws-billing-simulator", schemaKind)
	}

	var userVersion int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if userVersion != 32 {
		t.Fatalf("user_version = %d, want 32", userVersion)
	}
}

func assertMigrationRecorded(t *testing.T, db *sql.DB, wantVersion int, wantName string) {
	t.Helper()

	var version int
	var name, checksum, appliedAt string
	if err := db.QueryRowContext(
		context.Background(),
		`SELECT version, name, checksum, applied_at FROM schema_migrations WHERE version = ?`,
		wantVersion,
	).Scan(&version, &name, &checksum, &appliedAt); err != nil {
		t.Fatalf("read schema_migrations row %d: %v", wantVersion, err)
	}
	if version != wantVersion || name != wantName || checksum == "" || appliedAt == "" {
		t.Fatalf("schema migration row = (%d, %q, %q, %q), want populated v%d %s", version, name, checksum, appliedAt, wantVersion, wantName)
	}
}
