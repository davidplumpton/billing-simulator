package app

import (
	"fmt"
	"net/http"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

func exportFileFilterFromRequest(r *http.Request) exportFileFilterView {
	query := r.URL.Query()
	filter := exportFileFilterView{
		ExportType:         query.Get("export_type"),
		BillingPeriodStart: query.Get("billing_period_start"),
		BillingPeriodEnd:   query.Get("billing_period_end"),
		PayerAccountID:     query.Get("payer_account_id"),
		UsageAccountID:     query.Get("usage_account_id"),
		ViewerRole:         query.Get("viewer_role"),
		ViewerAccountID:    query.Get("viewer_account_id"),
		Limit:              query.Get("limit"),
		ApplyButton:        uiSubmitButton("Apply"),
		ClearPath:          "/exports",
	}
	filter.ExportTypeField = exportFileTypeSelect(filter.ExportType)
	filter.ViewerRoleField = viewerRoleSelectField(filter.ViewerRole, "Default viewer")
	filter.ViewerAccountField = viewerAccountIDField(filter.ViewerAccountID)
	filter.HasFilters = filter.ExportType != "" ||
		filter.BillingPeriodStart != "" ||
		filter.BillingPeriodEnd != "" ||
		filter.PayerAccountID != "" ||
		filter.UsageAccountID != "" ||
		filter.ViewerRole != "" ||
		filter.ViewerAccountID != "" ||
		filter.Limit != ""
	return filter
}

func curCSVGenerationFormFromRequest(r *http.Request) curCSVGenerationFormView {
	query := r.URL.Query()
	form := curCSVGenerationFormView{
		BillingPeriodStart: query.Get("billing_period_start"),
		BillingPeriodEnd:   query.Get("billing_period_end"),
		PayerAccountID:     query.Get("payer_account_id"),
		UsageAccountID:     query.Get("usage_account_id"),
		ViewerRole:         query.Get("viewer_role"),
		ViewerAccountID:    query.Get("viewer_account_id"),
		LineItemStatus:     query.Get("line_item_status"),
		Limit:              query.Get("limit"),
		GenerateButton:     uiSubmitButton("Generate CUR Export"),
	}
	form.ViewerRoleField = viewerRoleSelectField(form.ViewerRole, "Default viewer")
	form.ViewerAccountField = viewerAccountIDField(form.ViewerAccountID)
	form.LineItemStatusField = exportReconciliationLineItemStatusSelect(form.LineItemStatus)
	return form
}

func focusCSVGenerationFormFromRequest(r *http.Request) curCSVGenerationFormView {
	form := curCSVGenerationFormFromRequest(r)
	form.GenerateButton = uiSubmitButton("Generate FOCUS Export")
	return form
}

func exportFileTypeSelect(selected string) uiSelectFieldView {
	options := []uiSelectOptionView{
		{Value: "", Label: "All export types"},
		{Value: persistence.ExportFileTypeCURCSV, Label: "CUR CSV"},
		{Value: persistence.ExportFileTypeFOCUSCSV, Label: "FOCUS CSV"},
	}
	for idx := range options {
		options[idx].Selected = options[idx].Value == selected
	}
	return uiSelectFieldView{
		Label:   "Export Type",
		Name:    "export_type",
		Options: options,
	}
}

func exportFilesTable() uiTableView {
	return uiTable(uiTableHeaders("File", "Type", "Period", "Scope", "Provenance", "Size", "Checksum", "Updated", "Actions"), "No generated exports")
}

func exportFileRowsFromFiles(files []persistence.ExportFile, viewer exportViewerFields) []exportFileRowView {
	rows := make([]exportFileRowView, 0, len(files))
	for _, file := range files {
		rows = append(rows, exportFileRowViewFromFile(file, viewer))
	}
	return rows
}

func exportFileRowViewFromFile(file persistence.ExportFile, viewer exportViewerFields) exportFileRowView {
	row := exportFileRowView{
		Filename:           file.Filename,
		ExportType:         displayExportFileType(file.ExportType),
		Period:             displayExportFilePeriod(file.BillingPeriodStart, file.BillingPeriodEnd),
		PayerAccountID:     displayOptionalValue(file.PayerAccountID),
		UsageAccountID:     displayExportFileUsageAccount(file.UsageAccountID),
		LineItemStatus:     displayExportFileLineItemStatus(file.GenerationParameters["line_item_status"]),
		Size:               formatByteCount(file.SizeBytes),
		Checksum:           shortChecksum(file.ChecksumSHA256),
		GeneratedAt:        displayOptionalValue(file.GenerationParameters["generated_at"]),
		SourceBillID:       displayOptionalValue(file.GenerationParameters["source_bill_id"]),
		RowsWritten:        displayOptionalValue(file.GenerationParameters["rows_written"]),
		CreatedAt:          file.CreatedAt,
		UpdatedAt:          file.UpdatedAt,
		DownloadPath:       exportFileDownloadPathWithViewer(file.Filename, viewer),
		RegenerateFilename: file.Filename,
		ViewerRole:         viewer.Role,
		ViewerAccountID:    viewer.AccountID,
	}
	request, err := curCSVExportRequestFromExportFile(file)
	if err == nil {
		row.CanRegenerate = true
		row.ReconciliationPath = curExportReconciliationPathWithViewer(persistence.CURExportReconciliationRequest{
			BillingPeriodStart: request.BillingPeriodStart,
			BillingPeriodEnd:   request.BillingPeriodEnd,
			PayerAccountID:     request.PayerAccountID,
			UsageAccountID:     request.UsageAccountID,
			LineItemStatus:     request.LineItemStatus,
			Limit:              request.Limit,
		}, viewer)
	} else if _, err := focusCSVExportRequestFromExportFile(file); err == nil {
		row.CanRegenerate = true
	}
	return row
}

func displayExportFileType(exportType string) string {
	switch exportType {
	case persistence.ExportFileTypeCURCSV:
		return "CUR CSV"
	case persistence.ExportFileTypeFOCUSCSV:
		return "FOCUS CSV"
	default:
		return displayBillState(exportType)
	}
}

func displayExportFilePeriod(start, end string) string {
	start = strings.TrimSpace(start)
	end = strings.TrimSpace(end)
	if start == "" && end == "" {
		return "none"
	}
	return start + " to " + end
}

func displayExportFileUsageAccount(accountID string) string {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return "all accounts"
	}
	return accountID
}

func displayExportFileLineItemStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "all statuses"
	}
	return displayBillState(status)
}

func formatByteCount(value int64) string {
	if value < 0 {
		value = 0
	}
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	size := float64(value)
	unit := "B"
	for _, nextUnit := range []string{"KB", "MB", "GB", "TB"} {
		size = size / 1024
		unit = nextUnit
		if size < 1024 {
			break
		}
	}
	if size >= 10 {
		return fmt.Sprintf("%.0f %s", size, unit)
	}
	return fmt.Sprintf("%.1f %s", size, unit)
}

func shortChecksum(checksum string) string {
	checksum = strings.TrimSpace(checksum)
	if len(checksum) <= 12 {
		return checksum
	}
	return checksum[:12]
}
