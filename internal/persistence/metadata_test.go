package persistence

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestWorkspaceMetadataRepositoryCRUD(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewWorkspaceMetadataRepository(db)

	metadata, err := repo.Get(ctx, "schema_kind")
	if err != nil {
		t.Fatalf("Get(schema_kind) error = %v", err)
	}
	if metadata.Key != "schema_kind" || metadata.Value != "aws-billing-simulator" || metadata.UpdatedAt == "" {
		t.Fatalf("schema_kind metadata = %+v, want seeded metadata row", metadata)
	}

	if err := repo.Set(ctx, "workspace_name", "FinOps Lab"); err != nil {
		t.Fatalf("Set(workspace_name) create error = %v", err)
	}
	metadata, err = repo.Get(ctx, "workspace_name")
	if err != nil {
		t.Fatalf("Get(workspace_name) after create error = %v", err)
	}
	if metadata.Value != "FinOps Lab" {
		t.Fatalf("workspace_name value = %q, want FinOps Lab", metadata.Value)
	}

	if err := repo.Set(ctx, "workspace_name", "Consolidated Billing Lab"); err != nil {
		t.Fatalf("Set(workspace_name) update error = %v", err)
	}
	metadata, err = repo.Get(ctx, "workspace_name")
	if err != nil {
		t.Fatalf("Get(workspace_name) after update error = %v", err)
	}
	if metadata.Value != "Consolidated Billing Lab" {
		t.Fatalf("workspace_name value = %q, want Consolidated Billing Lab", metadata.Value)
	}

	if err := repo.Delete(ctx, "workspace_name"); err != nil {
		t.Fatalf("Delete(workspace_name) error = %v", err)
	}
	_, err = repo.Get(ctx, "workspace_name")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get(workspace_name) after delete error = %v, want sql.ErrNoRows", err)
	}
}

func TestWorkspaceMetadataRepositoryRejectsBlankKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewWorkspaceMetadataRepository(db)

	if _, err := repo.Get(ctx, " "); err == nil {
		t.Fatal("Get(blank key) error = nil, want validation error")
	}
	if err := repo.Set(ctx, "", "value"); err == nil {
		t.Fatal("Set(blank key) error = nil, want validation error")
	}
	if err := repo.Delete(ctx, "\t"); err == nil {
		t.Fatal("Delete(blank key) error = nil, want validation error")
	}
}

func openTestWorkspace(t *testing.T) *sql.DB {
	t.Helper()

	db, err := OpenWorkspace(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return db
}
