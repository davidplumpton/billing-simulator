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
	readRecord, readContent, err := repo.Read(ctx, record.Filename)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if readRecord.ID != record.ID || string(readContent) != string(content) {
		t.Fatalf("Read() = %+v/%q, want stored record and content", readRecord, readContent)
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
	listedByUsage, err := repo.List(ctx, ExportFileListRequest{
		ExportType:         ExportFileTypeCURCSV,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "111122223333",
	})
	if err != nil {
		t.Fatalf("List(usage account) error = %v", err)
	}
	if len(listedByUsage) != 1 || listedByUsage[0].Filename != record.Filename {
		t.Fatalf("List(usage account) = %+v, want the stored export", listedByUsage)
	}
	listedByOtherUsage, err := repo.List(ctx, ExportFileListRequest{
		ExportType:         ExportFileTypeCURCSV,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "444455556666",
	})
	if err != nil {
		t.Fatalf("List(other usage account) error = %v", err)
	}
	if len(listedByOtherUsage) != 0 {
		t.Fatalf("List(other usage account) = %+v, want no rows", listedByOtherUsage)
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

func TestExportFileRepositoryListOrdersRegeneratedFilesByUpdatedAt(t *testing.T) {
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
	older, err := repo.Write(ctx, ExportFileWriteRequest{
		Filename:           "cur-older-created.csv",
		ExportType:         ExportFileTypeCURCSV,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     AnyCompanyRetailManagementAccountID,
		Content:            []byte("older\n"),
	})
	if err != nil {
		t.Fatalf("Write(older) error = %v", err)
	}
	newer, err := repo.Write(ctx, ExportFileWriteRequest{
		Filename:           "cur-newer-created.csv",
		ExportType:         ExportFileTypeCURCSV,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     AnyCompanyRetailManagementAccountID,
		Content:            []byte("newer\n"),
	})
	if err != nil {
		t.Fatalf("Write(newer) error = %v", err)
	}

	olderCreatedAt := "2000-01-01T00:00:00.000Z"
	olderUpdatedAt := "2000-01-01T00:00:00.000Z"
	newerCreatedAt := "2001-01-01T00:00:00.000Z"
	newerUpdatedAt := "2001-01-01T00:00:00.000Z"
	for _, row := range []struct {
		filename  string
		createdAt string
		updatedAt string
	}{
		{filename: older.Filename, createdAt: olderCreatedAt, updatedAt: olderUpdatedAt},
		{filename: newer.Filename, createdAt: newerCreatedAt, updatedAt: newerUpdatedAt},
	} {
		if _, err := db.ExecContext(
			ctx,
			`UPDATE workspace_export_files SET created_at = ?, updated_at = ? WHERE filename = ?`,
			row.createdAt,
			row.updatedAt,
			row.filename,
		); err != nil {
			t.Fatalf("set deterministic timestamps for %s: %v", row.filename, err)
		}
	}

	listed, err := repo.List(ctx, ExportFileListRequest{ExportType: ExportFileTypeCURCSV})
	if err != nil {
		t.Fatalf("List(before regeneration) error = %v", err)
	}
	if len(listed) != 2 || listed[0].Filename != newer.Filename || listed[1].Filename != older.Filename {
		t.Fatalf("List(before regeneration) = %+v, want newer created export before older export", listed)
	}

	regenerated, err := repo.Write(ctx, ExportFileWriteRequest{
		Filename:           older.Filename,
		ExportType:         ExportFileTypeCURCSV,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     AnyCompanyRetailManagementAccountID,
		Content:            []byte("regenerated older\n"),
	})
	if err != nil {
		t.Fatalf("Write(regenerate older) error = %v", err)
	}
	if regenerated.CreatedAt != olderCreatedAt || regenerated.UpdatedAt == olderUpdatedAt {
		t.Fatalf("regenerated metadata = %+v, want original created_at and refreshed updated_at", regenerated)
	}

	listed, err = repo.List(ctx, ExportFileListRequest{ExportType: ExportFileTypeCURCSV})
	if err != nil {
		t.Fatalf("List(after regeneration) error = %v", err)
	}
	if len(listed) != 2 || listed[0].Filename != older.Filename || listed[1].Filename != newer.Filename {
		t.Fatalf("List(after regeneration) = %+v, want regenerated older export first", listed)
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
	if _, _, err := NewExportFileRepository(db, "").Read(ctx, "cur.csv"); err == nil {
		t.Fatal("Read(blank workspace) error = nil, want workspace validation error")
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
