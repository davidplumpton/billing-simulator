package persistence

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestWithTransactionCommitsSuccessfulWork(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)

	err := WithTransaction(ctx, db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(
			ctx,
			`INSERT INTO workspace_metadata (key, value) VALUES (?, ?)`,
			"transaction_commit_probe",
			"committed",
		)
		return err
	})
	if err != nil {
		t.Fatalf("WithTransaction() error = %v", err)
	}

	assertMetadataValue(t, db, "transaction_commit_probe", "committed")
}

func TestWithTransactionRollsBackOnError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	abortErr := errors.New("abort transaction")

	err := WithTransaction(ctx, db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO workspace_metadata (key, value) VALUES (?, ?)`,
			"transaction_rollback_probe",
			"rolled back",
		); err != nil {
			return err
		}
		return abortErr
	})
	if !errors.Is(err, abortErr) {
		t.Fatalf("WithTransaction() error = %v, want abortErr", err)
	}

	var count int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM workspace_metadata WHERE key = ?`,
		"transaction_rollback_probe",
	).Scan(&count); err != nil {
		t.Fatalf("count rollback probe: %v", err)
	}
	if count != 0 {
		t.Fatalf("rollback probe count = %d, want 0", count)
	}
}

func assertMetadataValue(t *testing.T, db *sql.DB, key, want string) {
	t.Helper()

	var got string
	if err := db.QueryRowContext(
		context.Background(),
		`SELECT value FROM workspace_metadata WHERE key = ?`,
		key,
	).Scan(&got); err != nil {
		t.Fatalf("read metadata %q: %v", key, err)
	}
	if got != want {
		t.Fatalf("metadata %q = %q, want %q", key, got, want)
	}
}
