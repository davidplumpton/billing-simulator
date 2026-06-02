package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// WorkspaceMetadata stores one workspace-level key/value setting.
type WorkspaceMetadata struct {
	Key       string
	Value     string
	UpdatedAt string
}

// WorkspaceMetadataRepository reads and writes workspace metadata rows.
type WorkspaceMetadataRepository struct {
	db *sql.DB
}

// NewWorkspaceMetadataRepository creates a repository backed by a workspace database.
func NewWorkspaceMetadataRepository(db *sql.DB) WorkspaceMetadataRepository {
	return WorkspaceMetadataRepository{db: db}
}

// Get reads a workspace metadata row by key.
func (r WorkspaceMetadataRepository) Get(ctx context.Context, key string) (WorkspaceMetadata, error) {
	if err := validateMetadataKey(key); err != nil {
		return WorkspaceMetadata{}, err
	}
	if r.db == nil {
		return WorkspaceMetadata{}, fmt.Errorf("database handle is required")
	}

	var metadata WorkspaceMetadata
	err := r.db.QueryRowContext(
		ctx,
		`SELECT key, value, updated_at FROM workspace_metadata WHERE key = ?`,
		key,
	).Scan(&metadata.Key, &metadata.Value, &metadata.UpdatedAt)
	if err != nil {
		return WorkspaceMetadata{}, err
	}
	return metadata, nil
}

// Set creates or updates a workspace metadata value.
func (r WorkspaceMetadataRepository) Set(ctx context.Context, key, value string) error {
	if err := validateMetadataKey(key); err != nil {
		return err
	}
	if r.db == nil {
		return fmt.Errorf("database handle is required")
	}

	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO workspace_metadata (key, value)
		 VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		 	value = excluded.value,
		 	updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
		key,
		value,
	)
	if err != nil {
		return fmt.Errorf("set workspace metadata %q: %w", key, err)
	}
	return nil
}

// Delete removes a workspace metadata value by key.
func (r WorkspaceMetadataRepository) Delete(ctx context.Context, key string) error {
	if err := validateMetadataKey(key); err != nil {
		return err
	}
	if r.db == nil {
		return fmt.Errorf("database handle is required")
	}

	if _, err := r.db.ExecContext(ctx, `DELETE FROM workspace_metadata WHERE key = ?`, key); err != nil {
		return fmt.Errorf("delete workspace metadata %q: %w", key, err)
	}
	return nil
}

func validateMetadataKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("workspace metadata key is required")
	}
	return nil
}
