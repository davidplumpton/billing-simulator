package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ExportFileTypeCURCSV = "cur_csv"

	defaultExportFileListLimit = 100
	maxExportFileListLimit     = 1_000
)

// ExportFile describes one generated file stored under a workspace exports directory.
type ExportFile struct {
	ID                   string
	Filename             string
	ExportType           string
	BillingPeriodStart   string
	BillingPeriodEnd     string
	PayerAccountID       string
	UsageAccountID       string
	SizeBytes            int64
	ChecksumSHA256       string
	GenerationParameters map[string]string
	CreatedAt            string
	UpdatedAt            string
}

// ExportFileWriteRequest provides file bytes and metadata for a generated workspace export.
type ExportFileWriteRequest struct {
	Filename             string
	ExportType           string
	BillingPeriodStart   string
	BillingPeriodEnd     string
	PayerAccountID       string
	UsageAccountID       string
	GenerationParameters map[string]string
	Content              []byte
}

// ExportFileListRequest filters generated export metadata rows.
type ExportFileListRequest struct {
	ExportType         string
	BillingPeriodStart string
	BillingPeriodEnd   string
	PayerAccountID     string
	Limit              int
}

// ExportFileRepository stores generated export files and their workspace metadata.
type ExportFileRepository struct {
	db            *sql.DB
	workspacePath string
}

// NewExportFileRepository creates a workspace export-file repository.
func NewExportFileRepository(db *sql.DB, workspacePath string) ExportFileRepository {
	return ExportFileRepository{
		db:            db,
		workspacePath: strings.TrimSpace(workspacePath),
	}
}

// Write stores export bytes under the workspace exports directory and upserts the metadata row.
func (r ExportFileRepository) Write(ctx context.Context, request ExportFileWriteRequest) (ExportFile, error) {
	if r.db == nil {
		return ExportFile{}, fmt.Errorf("database handle is required")
	}
	if strings.TrimSpace(r.workspacePath) == "" {
		return ExportFile{}, fmt.Errorf("workspace path is required")
	}
	request = normalizeExportFileWriteRequest(request)
	if err := validateExportFileWriteRequest(request); err != nil {
		return ExportFile{}, err
	}

	parametersJSON, err := marshalExportFileGenerationParameters(request.GenerationParameters)
	if err != nil {
		return ExportFile{}, err
	}
	checksum := sha256.Sum256(request.Content)
	checksumHex := hex.EncodeToString(checksum[:])

	exportsPath := WorkspaceExportsPath(r.workspacePath)
	if err := os.MkdirAll(exportsPath, 0o755); err != nil {
		return ExportFile{}, fmt.Errorf("create exports directory: %w", err)
	}
	finalPath := filepath.Join(exportsPath, request.Filename)
	tempFile, err := os.CreateTemp(exportsPath, "."+request.Filename+".tmp-*")
	if err != nil {
		return ExportFile{}, fmt.Errorf("create temporary export file: %w", err)
	}
	tempPath := tempFile.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := tempFile.Write(request.Content); err != nil {
		_ = tempFile.Close()
		return ExportFile{}, fmt.Errorf("write temporary export file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return ExportFile{}, fmt.Errorf("close temporary export file: %w", err)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return ExportFile{}, fmt.Errorf("store export file: %w", err)
	}
	committed = true

	id := exportFileID(request.Filename)
	if _, err := r.db.ExecContext(
		ctx,
		`INSERT INTO workspace_export_files (
			id,
			filename,
			export_type,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			usage_account_id,
			size_bytes,
			checksum_sha256,
			generation_parameters_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(filename) DO UPDATE SET
			id = excluded.id,
			export_type = excluded.export_type,
			billing_period_start = excluded.billing_period_start,
			billing_period_end = excluded.billing_period_end,
			payer_account_id = excluded.payer_account_id,
			usage_account_id = excluded.usage_account_id,
			size_bytes = excluded.size_bytes,
			checksum_sha256 = excluded.checksum_sha256,
			generation_parameters_json = excluded.generation_parameters_json,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
		id,
		request.Filename,
		request.ExportType,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		request.PayerAccountID,
		request.UsageAccountID,
		int64(len(request.Content)),
		checksumHex,
		parametersJSON,
	); err != nil {
		return ExportFile{}, fmt.Errorf("record export file metadata: %w", err)
	}
	return r.GetByFilename(ctx, request.Filename)
}

// GetByFilename reads one generated export metadata row.
func (r ExportFileRepository) GetByFilename(ctx context.Context, filename string) (ExportFile, error) {
	if r.db == nil {
		return ExportFile{}, fmt.Errorf("database handle is required")
	}
	filename = strings.TrimSpace(filename)
	if err := validateExportFilename(filename); err != nil {
		return ExportFile{}, err
	}

	row := r.db.QueryRowContext(
		ctx,
		`SELECT
			id,
			filename,
			export_type,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			usage_account_id,
			size_bytes,
			checksum_sha256,
			generation_parameters_json,
			created_at,
			updated_at
		 FROM workspace_export_files
		 WHERE filename = ?`,
		filename,
	)
	return scanExportFile(row)
}

// List returns generated export metadata rows ordered with the newest row first.
func (r ExportFileRepository) List(ctx context.Context, request ExportFileListRequest) ([]ExportFile, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeExportFileListRequest(request)
	if err := validateExportFileListRequest(request); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			filename,
			export_type,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			usage_account_id,
			size_bytes,
			checksum_sha256,
			generation_parameters_json,
			created_at,
			updated_at
		 FROM workspace_export_files
		 WHERE (? = '' OR export_type = ?)
		   AND (? = '' OR billing_period_start = ?)
		   AND (? = '' OR billing_period_end = ?)
		   AND (? = '' OR payer_account_id = ?)
		 ORDER BY created_at DESC, filename DESC
		 LIMIT ?`,
		request.ExportType,
		request.ExportType,
		request.BillingPeriodStart,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		request.BillingPeriodEnd,
		request.PayerAccountID,
		request.PayerAccountID,
		request.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list export file metadata: %w", err)
	}
	defer rows.Close()

	files := []ExportFile{}
	for rows.Next() {
		file, err := scanExportFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate export file metadata: %w", err)
	}
	return files, nil
}

func normalizeExportFileWriteRequest(request ExportFileWriteRequest) ExportFileWriteRequest {
	request.Filename = strings.TrimSpace(request.Filename)
	request.ExportType = strings.TrimSpace(request.ExportType)
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	request.UsageAccountID = strings.TrimSpace(request.UsageAccountID)
	request.GenerationParameters = normalizeExportFileGenerationParameters(request.GenerationParameters)
	return request
}

func normalizeExportFileListRequest(request ExportFileListRequest) ExportFileListRequest {
	request.ExportType = strings.TrimSpace(request.ExportType)
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	if request.Limit <= 0 {
		request.Limit = defaultExportFileListLimit
	}
	if request.Limit > maxExportFileListLimit {
		request.Limit = maxExportFileListLimit
	}
	return request
}

func validateExportFileWriteRequest(request ExportFileWriteRequest) error {
	if err := validateExportFilename(request.Filename); err != nil {
		return err
	}
	if err := validateExportFileType(request.ExportType); err != nil {
		return err
	}
	return validateExportFilePeriod(request.BillingPeriodStart, request.BillingPeriodEnd)
}

func validateExportFileListRequest(request ExportFileListRequest) error {
	if request.ExportType != "" {
		if err := validateExportFileType(request.ExportType); err != nil {
			return err
		}
	}
	return validateExportFilePeriod(request.BillingPeriodStart, request.BillingPeriodEnd)
}

func validateExportFilename(filename string) error {
	if filename == "" {
		return fmt.Errorf("export filename is required")
	}
	if filename == "." || filename == ".." || filename != filepath.Base(filename) || strings.ContainsAny(filename, `/\`) {
		return fmt.Errorf("export filename %q must not contain path separators", filename)
	}
	if len(filename) > 200 {
		return fmt.Errorf("export filename %q is too long", filename)
	}
	for _, r := range filename {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' ||
			r == '_' ||
			r == '.' {
			continue
		}
		return fmt.Errorf("export filename %q contains unsupported character %q", filename, r)
	}
	return nil
}

func validateExportFileType(exportType string) error {
	if exportType == "" {
		return fmt.Errorf("export type is required")
	}
	for i, r := range exportType {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("export type %q contains unsupported character %q at position %d", exportType, r, i)
	}
	return nil
}

func validateExportFilePeriod(periodStart, periodEnd string) error {
	if (periodStart == "") != (periodEnd == "") {
		return fmt.Errorf("export billing period start and end must be provided together")
	}
	if periodStart == "" {
		return nil
	}
	if _, err := time.Parse(time.DateOnly, periodStart); err != nil {
		return fmt.Errorf("export billing period start must use YYYY-MM-DD: %w", err)
	}
	if _, err := time.Parse(time.DateOnly, periodEnd); err != nil {
		return fmt.Errorf("export billing period end must use YYYY-MM-DD: %w", err)
	}
	if periodStart >= periodEnd {
		return fmt.Errorf("export billing period start must be before end")
	}
	return nil
}

func normalizeExportFileGenerationParameters(parameters map[string]string) map[string]string {
	if len(parameters) == 0 {
		return map[string]string{}
	}
	normalized := map[string]string{}
	for key, value := range parameters {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		normalized[key] = strings.TrimSpace(value)
	}
	return normalized
}

func marshalExportFileGenerationParameters(parameters map[string]string) (string, error) {
	normalized := normalizeExportFileGenerationParameters(parameters)
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode export generation parameters: %w", err)
	}
	return string(data), nil
}

type exportFileScanner interface {
	Scan(dest ...any) error
}

func scanExportFile(scanner exportFileScanner) (ExportFile, error) {
	var file ExportFile
	var parametersJSON string
	if err := scanner.Scan(
		&file.ID,
		&file.Filename,
		&file.ExportType,
		&file.BillingPeriodStart,
		&file.BillingPeriodEnd,
		&file.PayerAccountID,
		&file.UsageAccountID,
		&file.SizeBytes,
		&file.ChecksumSHA256,
		&parametersJSON,
		&file.CreatedAt,
		&file.UpdatedAt,
	); err != nil {
		return ExportFile{}, err
	}
	if err := json.Unmarshal([]byte(parametersJSON), &file.GenerationParameters); err != nil {
		return ExportFile{}, fmt.Errorf("decode export generation parameters for %q: %w", file.Filename, err)
	}
	if file.GenerationParameters == nil {
		file.GenerationParameters = map[string]string{}
	}
	return file, nil
}

func exportFileID(filename string) string {
	sum := sha256.Sum256([]byte(filename))
	return "exp_" + hex.EncodeToString(sum[:8])
}
