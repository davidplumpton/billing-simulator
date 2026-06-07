package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExportFileRepositoryWritesFileAndMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspacePath := t.TempDir()
	db, err := OpenWorkspace(ctx, workspacePath)
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	repo := NewExportFileRepository(db, workspacePath)
	content := []byte("header\nrow\n")
	record, err := repo.Write(ctx, ExportFileWriteRequest{
		Filename:           "cur-2026-02-01-2026-03-01-999988887777.csv",
		ExportType:         ExportFileTypeCURCSV,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "111122223333",
		GenerationParameters: map[string]string{
			" generated_at ": " 2026-03-02T09:30:00Z ",
			"limit":          "1000",
		},
		Content: content,
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if record.ID == "" ||
		record.Filename != "cur-2026-02-01-2026-03-01-999988887777.csv" ||
		record.ExportType != ExportFileTypeCURCSV ||
		record.BillingPeriodStart != "2026-02-01" ||
		record.BillingPeriodEnd != "2026-03-01" ||
		record.PayerAccountID != AnyCompanyRetailManagementAccountID ||
		record.UsageAccountID != "111122223333" ||
		record.SizeBytes != int64(len(content)) ||
		record.CreatedAt == "" ||
		record.UpdatedAt == "" {
		t.Fatalf("export metadata = %+v, want populated CUR export record", record)
	}
	if record.GenerationParameters["generated_at"] != "2026-03-02T09:30:00Z" ||
		record.GenerationParameters["limit"] != "1000" {
		t.Fatalf("generation parameters = %+v, want trimmed values", record.GenerationParameters)
	}
	sum := sha256.Sum256(content)
	if record.ChecksumSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("checksum = %q, want sha256 of content", record.ChecksumSHA256)
	}

	storedPath := filepath.Join(WorkspaceExportsPath(workspacePath), record.Filename)
	storedContent, err := os.ReadFile(storedPath)
	if err != nil {
		t.Fatalf("read stored export file: %v", err)
	}
	if string(storedContent) != string(content) {
		t.Fatalf("stored export content = %q, want %q", storedContent, content)
	}

	listed, err := repo.List(ctx, ExportFileListRequest{
		ExportType:         ExportFileTypeCURCSV,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     AnyCompanyRetailManagementAccountID,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].Filename != record.Filename {
		t.Fatalf("List() = %+v, want the stored export", listed)
	}

	updatedContent := []byte("new-header\n")
	updated, err := repo.Write(ctx, ExportFileWriteRequest{
		Filename:           record.Filename,
		ExportType:         ExportFileTypeCURCSV,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     AnyCompanyRetailManagementAccountID,
		Content:            updatedContent,
	})
	if err != nil {
		t.Fatalf("Write(update) error = %v", err)
	}
	if updated.ID != record.ID || updated.SizeBytes != int64(len(updatedContent)) {
		t.Fatalf("updated metadata = %+v, want same ID and updated size", updated)
	}
	storedContent, err = os.ReadFile(storedPath)
	if err != nil {
		t.Fatalf("read updated export file: %v", err)
	}
	if string(storedContent) != string(updatedContent) {
		t.Fatalf("updated export content = %q, want %q", storedContent, updatedContent)
	}
}

func TestExportFileRepositoryValidatesRequests(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspacePath := t.TempDir()
	db, err := OpenWorkspace(ctx, workspacePath)
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	repo := NewExportFileRepository(db, workspacePath)

	if _, err := NewExportFileRepository(nil, workspacePath).Write(ctx, ExportFileWriteRequest{}); err == nil {
		t.Fatal("Write(nil db) error = nil, want database validation error")
	}
	if _, err := NewExportFileRepository(db, "").Write(ctx, ExportFileWriteRequest{}); err == nil {
		t.Fatal("Write(blank workspace) error = nil, want workspace validation error")
	}
	if _, err := repo.Write(ctx, ExportFileWriteRequest{
		Filename:   "../cur.csv",
		ExportType: ExportFileTypeCURCSV,
	}); err == nil {
		t.Fatal("Write(path traversal filename) error = nil, want validation error")
	}
	if _, err := repo.Write(ctx, ExportFileWriteRequest{
		Filename:   "cur.csv",
		ExportType: "CUR CSV",
	}); err == nil {
		t.Fatal("Write(invalid export type) error = nil, want validation error")
	}
	if _, err := repo.Write(ctx, ExportFileWriteRequest{
		Filename:           "cur.csv",
		ExportType:         ExportFileTypeCURCSV,
		BillingPeriodStart: "2026-02-01",
	}); err == nil {
		t.Fatal("Write(period start only) error = nil, want validation error")
	}
	if _, err := repo.GetByFilename(ctx, "missing.csv"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetByFilename(missing) error = %v, want sql.ErrNoRows", err)
	}
}
