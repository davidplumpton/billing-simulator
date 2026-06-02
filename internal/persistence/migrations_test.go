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
	if count != 2 {
		t.Fatalf("schema_migrations count = %d, want 2", count)
	}

	assertMigrationRecorded(t, db, 1, "workspace_metadata")
	assertMigrationRecorded(t, db, 2, "price_catalog_items")

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
	if userVersion != 2 {
		t.Fatalf("user_version = %d, want 2", userVersion)
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
