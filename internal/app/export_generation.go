package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"aws-billing-simulator/internal/billingvisibility"
	"aws-billing-simulator/internal/persistence"
)

// persistCURCSVExportFile generates CUR-like CSV bytes and records them in the workspace export inventory.
func (h exportsHandler) persistCURCSVExportFile(ctx context.Context, request persistence.CURCSVExportRequest) (persistence.ExportFile, persistence.CURCSVExportResult, error) {
	var body bytes.Buffer
	result, err := h.cur.WriteCSVExport(ctx, &body, request)
	if err != nil {
		return persistence.ExportFile{}, persistence.CURCSVExportResult{}, err
	}
	record, err := h.writeCURCSVExportFile(ctx, request, body.Bytes(), result)
	if err != nil {
		return persistence.ExportFile{}, persistence.CURCSVExportResult{}, exportStorageError{err: err}
	}
	return record, result, nil
}

// writeCSVDownloadHeaders sets the shared headers for direct CSV downloads.
func writeCSVDownloadHeaders(w http.ResponseWriter, filename string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
}

func (h exportsHandler) writeCURCSVExportFile(ctx context.Context, request persistence.CURCSVExportRequest, content []byte, result persistence.CURCSVExportResult) (persistence.ExportFile, error) {
	return h.exportFiles.Write(ctx, persistence.ExportFileWriteRequest{
		Filename:             curCSVExportFilename(request),
		ExportType:           persistence.ExportFileTypeCURCSV,
		BillingPeriodStart:   request.BillingPeriodStart,
		BillingPeriodEnd:     request.BillingPeriodEnd,
		PayerAccountID:       request.PayerAccountID,
		UsageAccountID:       request.UsageAccountID,
		GenerationParameters: curCSVExportGenerationParameters(request, result),
		Content:              content,
	})
}

func (h exportsHandler) persistFOCUSCSVExportFile(ctx context.Context, request persistence.CURCSVExportRequest) (persistence.ExportFile, persistence.CURCSVExportResult, error) {
	var body bytes.Buffer
	result, err := h.cur.WriteFOCUSCSVExport(ctx, &body, request)
	if err != nil {
		return persistence.ExportFile{}, persistence.CURCSVExportResult{}, err
	}
	record, err := h.writeFOCUSCSVExportFile(ctx, request, body.Bytes(), result)
	if err != nil {
		return persistence.ExportFile{}, persistence.CURCSVExportResult{}, exportStorageError{err: err}
	}
	return record, result, nil
}

func (h exportsHandler) writeFOCUSCSVExportFile(ctx context.Context, request persistence.CURCSVExportRequest, content []byte, result persistence.CURCSVExportResult) (persistence.ExportFile, error) {
	return h.exportFiles.Write(ctx, persistence.ExportFileWriteRequest{
		Filename:             focusCSVExportFilename(request),
		ExportType:           persistence.ExportFileTypeFOCUSCSV,
		BillingPeriodStart:   request.BillingPeriodStart,
		BillingPeriodEnd:     request.BillingPeriodEnd,
		PayerAccountID:       request.PayerAccountID,
		UsageAccountID:       request.UsageAccountID,
		GenerationParameters: focusCSVExportGenerationParameters(request, result),
		Content:              content,
	})
}

func (h exportsHandler) regenerateExportFile(ctx context.Context, file persistence.ExportFile, policy billingvisibility.Policy) (persistence.ExportFile, persistence.CURCSVExportResult, error) {
	switch file.ExportType {
	case persistence.ExportFileTypeCURCSV:
		request, err := curCSVExportRequestFromExportFile(file)
		if err != nil {
			return persistence.ExportFile{}, persistence.CURCSVExportResult{}, err
		}
		request, err = h.scopedCURCSVExportRequest(ctx, request, policy)
		if err != nil {
			return persistence.ExportFile{}, persistence.CURCSVExportResult{}, err
		}
		return h.persistCURCSVExportFile(ctx, request)
	case persistence.ExportFileTypeFOCUSCSV:
		request, err := focusCSVExportRequestFromExportFile(file)
		if err != nil {
			return persistence.ExportFile{}, persistence.CURCSVExportResult{}, err
		}
		request, err = h.scopedCURCSVExportRequest(ctx, request, policy)
		if err != nil {
			return persistence.ExportFile{}, persistence.CURCSVExportResult{}, err
		}
		return h.persistFOCUSCSVExportFile(ctx, request)
	default:
		return persistence.ExportFile{}, persistence.CURCSVExportResult{}, fmt.Errorf("export type %q cannot be regenerated", file.ExportType)
	}
}
