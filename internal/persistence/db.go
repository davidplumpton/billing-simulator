package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	sqliteDriver         = "sqlite"
	workspaceDBFile      = "simulator.db"
	workspaceExportsDir  = "exports"
	workspaceBusyTimeout = 5 * time.Second
)

// WorkspaceDBPath returns the SQLite database path inside a workspace.
func WorkspaceDBPath(workspacePath string) string {
	return filepath.Join(workspacePath, workspaceDBFile)
}

// WorkspaceExportsPath returns the directory used for generated export files.
func WorkspaceExportsPath(workspacePath string) string {
	return filepath.Join(workspacePath, workspaceExportsDir)
}

// OpenWorkspace creates the workspace database if needed and applies migrations.
func OpenWorkspace(ctx context.Context, workspacePath string) (*sql.DB, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return nil, fmt.Errorf("workspace path is required")
	}
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace directory: %w", err)
	}

	dsn, err := workspaceDSN(WorkspaceDBPath(workspacePath))
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(sqliteDriver, dsn)
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
	if err := NewPriceCatalogRepository(db).Validate(ctx); err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("validate price catalog: %w; close database: %v", err, closeErr)
		}
		return nil, fmt.Errorf("validate price catalog: %w", err)
	}

	return db, nil
}

// workspaceDSN builds a SQLite URI that applies connection-local pragmas.
func workspaceDSN(dbPath string) (string, error) {
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return "", fmt.Errorf("resolve workspace database path: %w", err)
	}

	query := url.Values{}
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", int(workspaceBusyTimeout/time.Millisecond)))
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")

	uri := url.URL{
		Scheme:   "file",
		Path:     absPath,
		RawQuery: query.Encode(),
	}
	return uri.String(), nil
}
