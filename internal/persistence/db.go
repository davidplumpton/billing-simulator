package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	sqliteDriver    = "sqlite"
	workspaceDBFile = "simulator.db"
)

// WorkspaceDBPath returns the SQLite database path inside a workspace.
func WorkspaceDBPath(workspacePath string) string {
	return filepath.Join(workspacePath, workspaceDBFile)
}

// OpenWorkspace creates the workspace database if needed and applies migrations.
func OpenWorkspace(ctx context.Context, workspacePath string) (*sql.DB, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return nil, fmt.Errorf("workspace path is required")
	}
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace directory: %w", err)
	}

	db, err := sql.Open(sqliteDriver, WorkspaceDBPath(workspacePath))
	if err != nil {
		return nil, fmt.Errorf("open workspace database: %w", err)
	}
	if err := ApplyMigrations(ctx, db); err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("apply migrations: %w; close database: %v", err, closeErr)
		}
		return nil, fmt.Errorf("apply migrations: %w", err)
	}

	return db, nil
}
