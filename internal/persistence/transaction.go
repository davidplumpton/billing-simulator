package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// WithTransaction runs fn in a short database transaction and commits on success.
func WithTransaction(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	if db == nil {
		return fmt.Errorf("database handle is required")
	}
	if fn == nil {
		return fmt.Errorf("transaction function is required")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback transaction: %w", rollbackErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
