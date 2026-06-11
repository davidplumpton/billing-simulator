package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/billingvisibility"
	"aws-billing-simulator/internal/persistence"
)

func curCSVExportRequestFromQuery(r *http.Request) (persistence.CURCSVExportRequest, error) {
	return curCSVExportRequestFromValues(r.URL.Query())
}

func curCSVExportRequestFromForm(r *http.Request) (persistence.CURCSVExportRequest, error) {
	return curCSVExportRequestFromValues(r.PostForm)
}

func curCSVExportRequestFromValues(values url.Values) (persistence.CURCSVExportRequest, error) {
	request := persistence.CURCSVExportRequest{
		BillingPeriodStart: values.Get("billing_period_start"),
		BillingPeriodEnd:   values.Get("billing_period_end"),
		PayerAccountID:     values.Get("payer_account_id"),
		UsageAccountID:     values.Get("usage_account_id"),
		LineItemStatus:     values.Get("line_item_status"),
	}
	if rawLimit := strings.TrimSpace(values.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return persistence.CURCSVExportRequest{}, fmt.Errorf("limit must be an integer")
		}
		request.Limit = limit
	}
	return request, nil
}

func curExportReconciliationRequestFromQuery(r *http.Request) (persistence.CURExportReconciliationRequest, error) {
	query := r.URL.Query()
	request := persistence.CURExportReconciliationRequest{
		BillingPeriodStart: query.Get("billing_period_start"),
		BillingPeriodEnd:   query.Get("billing_period_end"),
		PayerAccountID:     query.Get("payer_account_id"),
		UsageAccountID:     query.Get("usage_account_id"),
		LineItemStatus:     query.Get("line_item_status"),
	}
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return persistence.CURExportReconciliationRequest{}, fmt.Errorf("limit must be an integer")
		}
		request.Limit = limit
	}
	return request, nil
}

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

func (h exportsHandler) exportPolicyFromValues(ctx context.Context, values url.Values) (billingvisibility.Policy, error) {
	resolution, err := resolveViewerPolicy(ctx, h.db, exportViewerFieldsFromValues(values), viewerPolicyResolveOptions{
		DefaultRole:  billingvisibility.RoleManagementAccount,
		RequiredView: billingvisibility.ViewExports,
		PermissionErr: func(policy billingvisibility.Policy) error {
			return exportAccessError{err: fmt.Errorf("billing role %q cannot view exports", policy.Role)}
		},
	})
	if err != nil {
		return billingvisibility.Policy{}, err
	}
	return resolution.Policy, nil
}

func (h exportsHandler) scopedExportFileListRequest(ctx context.Context, request persistence.ExportFileListRequest, policy billingvisibility.Policy) (persistence.ExportFileListRequest, error) {
	defaultPayerAccountID, err := defaultBillingPayerAccountID(ctx, h.db, "")
	if err != nil {
		return persistence.ExportFileListRequest{}, err
	}
	if payerAccountID, ok := policy.PayerAccountFilter(); ok {
		if request.PayerAccountID != "" && request.PayerAccountID != payerAccountID {
			return persistence.ExportFileListRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot list exports for payer account %q", policy.Role, request.PayerAccountID)}
		}
		request.PayerAccountID = payerAccountID
	}
	if usageAccountID, ok := policy.UsageAccountFilter(); ok {
		if request.PayerAccountID != "" && defaultPayerAccountID != "" && request.PayerAccountID != defaultPayerAccountID {
			return persistence.ExportFileListRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot list exports for payer account %q", policy.Role, request.PayerAccountID)}
		}
		if request.UsageAccountID != "" && request.UsageAccountID != usageAccountID {
			return persistence.ExportFileListRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot list exports for usage account %q", policy.Role, request.UsageAccountID)}
		}
		if request.PayerAccountID == "" {
			request.PayerAccountID = defaultPayerAccountID
		}
		request.UsageAccountID = usageAccountID
	}
	return request, nil
}

func (h exportsHandler) scopedCURCSVExportRequest(ctx context.Context, request persistence.CURCSVExportRequest, policy billingvisibility.Policy) (persistence.CURCSVExportRequest, error) {
	defaultPayerAccountID, err := defaultBillingPayerAccountID(ctx, h.db, "")
	if err != nil {
		return persistence.CURCSVExportRequest{}, err
	}
	request.Visibility.PayerAccountID = strings.TrimSpace(request.Visibility.PayerAccountID)
	request.Visibility.UsageAccountID = strings.TrimSpace(request.Visibility.UsageAccountID)
	if request.Visibility.PayerAccountID != "" && request.Visibility.UsageAccountID != "" {
		return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("CUR export visibility cannot be scoped to both payer and usage accounts")}
	}
	if request.Visibility.UsageAccountID != "" {
		if !policy.AllowsUsageAccount(request.Visibility.UsageAccountID) {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export usage account %q", policy.Role, request.Visibility.UsageAccountID)}
		}
		if request.UsageAccountID != "" && request.UsageAccountID != request.Visibility.UsageAccountID {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export usage account %q", policy.Role, request.UsageAccountID)}
		}
		request.UsageAccountID = request.Visibility.UsageAccountID
		request.Visibility = persistence.BillingVisibilityFilter{UsageAccountID: request.Visibility.UsageAccountID}
	}
	if request.Visibility.PayerAccountID != "" {
		if !policy.AllowsPayerAccount(request.Visibility.PayerAccountID) {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export payer account %q", policy.Role, request.Visibility.PayerAccountID)}
		}
		if request.PayerAccountID != "" && request.PayerAccountID != request.Visibility.PayerAccountID {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export payer account %q", policy.Role, request.PayerAccountID)}
		}
		request.PayerAccountID = request.Visibility.PayerAccountID
		request.Visibility = persistence.BillingVisibilityFilter{PayerAccountID: request.Visibility.PayerAccountID}
	}
	if payerAccountID, ok := policy.PayerAccountFilter(); ok {
		if request.PayerAccountID != "" && request.PayerAccountID != payerAccountID {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export payer account %q", policy.Role, request.PayerAccountID)}
		}
		request.PayerAccountID = payerAccountID
		if request.Visibility.UsageAccountID == "" {
			request.Visibility = persistence.BillingVisibilityFilter{PayerAccountID: payerAccountID}
		}
	}
	if usageAccountID, ok := policy.UsageAccountFilter(); ok {
		if request.Visibility.PayerAccountID != "" {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export payer account %q", policy.Role, request.Visibility.PayerAccountID)}
		}
		if request.PayerAccountID != "" && defaultPayerAccountID != "" && request.PayerAccountID != defaultPayerAccountID {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export payer account %q", policy.Role, request.PayerAccountID)}
		}
		if request.UsageAccountID != "" && request.UsageAccountID != usageAccountID {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export usage account %q", policy.Role, request.UsageAccountID)}
		}
		if request.PayerAccountID == "" {
			request.PayerAccountID = defaultPayerAccountID
		}
		request.UsageAccountID = usageAccountID
		request.Visibility = persistence.BillingVisibilityFilter{UsageAccountID: usageAccountID}
	}
	return request, nil
}

func (h exportsHandler) scopedCURExportReconciliationRequest(ctx context.Context, request persistence.CURExportReconciliationRequest, policy billingvisibility.Policy) (persistence.CURExportReconciliationRequest, error) {
	csvRequest, err := h.scopedCURCSVExportRequest(ctx, persistence.CURCSVExportRequest{
		BillingPeriodStart: request.BillingPeriodStart,
		BillingPeriodEnd:   request.BillingPeriodEnd,
		PayerAccountID:     request.PayerAccountID,
		UsageAccountID:     request.UsageAccountID,
		LineItemStatus:     request.LineItemStatus,
		Limit:              request.Limit,
	}, policy)
	if err != nil {
		return persistence.CURExportReconciliationRequest{}, err
	}
	request.PayerAccountID = csvRequest.PayerAccountID
	request.UsageAccountID = csvRequest.UsageAccountID
	request.Visibility = csvRequest.Visibility
	return request, nil
}

// visibleExportFilesForPolicy removes stored files whose generation scope is broader than the viewer can inspect.
func visibleExportFilesForPolicy(files []persistence.ExportFile, policy billingvisibility.Policy) []persistence.ExportFile {
	visible := make([]persistence.ExportFile, 0, len(files))
	for _, file := range files {
		if err := ensureExportFileVisibleToPolicy(policy, file); err == nil {
			visible = append(visible, file)
		}
	}
	return visible
}

func ensureExportFileVisibleToPolicy(policy billingvisibility.Policy, file persistence.ExportFile) error {
	if !policy.AllowsView(billingvisibility.ViewExports) {
		return exportAccessError{err: fmt.Errorf("billing role %q cannot view exports", policy.Role)}
	}
	if payerAccountID, ok := policy.PayerAccountFilter(); ok {
		if file.PayerAccountID != payerAccountID {
			return exportAccessError{err: fmt.Errorf("billing role %q cannot access export for payer account %q", policy.Role, file.PayerAccountID)}
		}
		return nil
	}
	if usageAccountID, ok := policy.UsageAccountFilter(); ok {
		if file.UsageAccountID == "" {
			return exportAccessError{err: fmt.Errorf("billing role %q cannot access all-account exports", policy.Role)}
		}
		if file.UsageAccountID != usageAccountID {
			return exportAccessError{err: fmt.Errorf("billing role %q cannot access export for usage account %q", policy.Role, file.UsageAccountID)}
		}
		scope, accountID, err := exportFileVisibilityScope(file)
		if err != nil {
			return exportAccessError{err: err}
		}
		if scope != exportVisibilityScopeUsageAccount || accountID != usageAccountID {
			return exportAccessError{err: fmt.Errorf("billing role %q cannot access export generated outside usage account scope %q", policy.Role, usageAccountID)}
		}
		return nil
	}
	return nil
}

func exportHTTPStatus(err error) int {
	var accessErr exportAccessError
	if errors.As(err, &accessErr) {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

func exportGenerationHTTPStatus(err error) int {
	var accessErr exportAccessError
	if errors.As(err, &accessErr) {
		return http.StatusForbidden
	}
	var storageErr exportStorageError
	if errors.As(err, &storageErr) {
		return http.StatusInternalServerError
	}
	return http.StatusBadRequest
}

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

func exportFileListRequestFromFilter(filter exportFileFilterView) (persistence.ExportFileListRequest, error) {
	request := persistence.ExportFileListRequest{
		ExportType:         filter.ExportType,
		BillingPeriodStart: filter.BillingPeriodStart,
		BillingPeriodEnd:   filter.BillingPeriodEnd,
		PayerAccountID:     filter.PayerAccountID,
		UsageAccountID:     filter.UsageAccountID,
	}
	if rawLimit := strings.TrimSpace(filter.Limit); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return persistence.ExportFileListRequest{}, fmt.Errorf("limit must be an integer")
		}
		request.Limit = limit
	}
	return request, nil
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

func curCSVExportRequestFromExportFile(file persistence.ExportFile) (persistence.CURCSVExportRequest, error) {
	return csvExportRequestFromExportFile(file, persistence.ExportFileTypeCURCSV, "CUR CSV")
}

func focusCSVExportRequestFromExportFile(file persistence.ExportFile) (persistence.CURCSVExportRequest, error) {
	return csvExportRequestFromExportFile(file, persistence.ExportFileTypeFOCUSCSV, "FOCUS CSV")
}

func csvExportRequestFromExportFile(file persistence.ExportFile, exportType, label string) (persistence.CURCSVExportRequest, error) {
	if file.ExportType != exportType {
		return persistence.CURCSVExportRequest{}, fmt.Errorf("export type %q cannot be regenerated as %s", file.ExportType, label)
	}
	visibility, err := curCSVExportVisibilityFromGenerationParameters(file.GenerationParameters)
	if err != nil {
		return persistence.CURCSVExportRequest{}, err
	}
	request := persistence.CURCSVExportRequest{
		BillingPeriodStart: file.BillingPeriodStart,
		BillingPeriodEnd:   file.BillingPeriodEnd,
		PayerAccountID:     file.PayerAccountID,
		UsageAccountID:     file.UsageAccountID,
		LineItemStatus:     file.GenerationParameters["line_item_status"],
		Visibility:         visibility,
	}
	if rawLimit := strings.TrimSpace(file.GenerationParameters["limit"]); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return persistence.CURCSVExportRequest{}, fmt.Errorf("stored export limit must be an integer")
		}
		request.Limit = limit
	}
	return request, nil
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

func exportFileContentType(exportType string) string {
	switch exportType {
	case persistence.ExportFileTypeCURCSV, persistence.ExportFileTypeFOCUSCSV:
		return "text/csv; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func exportReconciliationFilterFromRequest(r *http.Request) exportReconciliationFilterView {
	query := r.URL.Query()
	filter := exportReconciliationFilterView{
		BillingPeriodStart: query.Get("billing_period_start"),
		BillingPeriodEnd:   query.Get("billing_period_end"),
		PayerAccountID:     query.Get("payer_account_id"),
		UsageAccountID:     query.Get("usage_account_id"),
		ViewerRole:         query.Get("viewer_role"),
		ViewerAccountID:    query.Get("viewer_account_id"),
		LineItemStatus:     query.Get("line_item_status"),
		Limit:              query.Get("limit"),
		ApplyButton:        uiSubmitButton("Run Report"),
		ClearPath:          "/exports/reconciliation",
	}
	filter.ViewerRoleField = viewerRoleSelectField(filter.ViewerRole, "Default viewer")
	filter.ViewerAccountField = viewerAccountIDField(filter.ViewerAccountID)
	filter.LineItemStatusField = exportReconciliationLineItemStatusSelect(filter.LineItemStatus)
	filter.HasFilters = filter.BillingPeriodStart != "" ||
		filter.BillingPeriodEnd != "" ||
		filter.PayerAccountID != "" ||
		filter.UsageAccountID != "" ||
		filter.ViewerRole != "" ||
		filter.ViewerAccountID != "" ||
		filter.LineItemStatus != "" ||
		filter.Limit != ""
	return filter
}

func exportReconciliationLineItemStatusSelect(selected string) uiSelectFieldView {
	options := []uiSelectOptionView{
		{Value: "", Label: "All statuses"},
		{Value: "final", Label: "Final"},
		{Value: "estimated", Label: "Estimated"},
	}
	for idx := range options {
		options[idx].Selected = options[idx].Value == selected
	}
	return uiSelectFieldView{
		Label:   "Line Item Status",
		Name:    "line_item_status",
		Options: options,
	}
}

func curCSVExportFilename(request persistence.CURCSVExportRequest) string {
	return csvExportFilename("cur", request)
}

func focusCSVExportFilename(request persistence.CURCSVExportRequest) string {
	return csvExportFilename("focus", request)
}

func csvExportFilename(prefix string, request persistence.CURCSVExportRequest) string {
	limitPart := "default"
	if request.Limit > 0 {
		limitPart = strconv.Itoa(request.Limit)
	}
	parts := []string{
		prefix,
		safeCSVFilenamePart(request.BillingPeriodStart, "period-start"),
		safeCSVFilenamePart(request.BillingPeriodEnd, "period-end"),
		"payer",
		safeCSVFilenamePart(request.PayerAccountID, "payer"),
		"usage",
		safeCSVFilenamePart(request.UsageAccountID, "all-accounts"),
		"status",
		safeCSVFilenamePart(request.LineItemStatus, "all-statuses"),
		"limit",
		safeCSVFilenamePart(limitPart, "default"),
	}
	if request.Visibility.UsageAccountID != "" {
		parts = append(parts, "visibility", "usage", safeCSVFilenamePart(request.Visibility.UsageAccountID, "usage-account"))
	}
	return strings.Join(parts, "-") + ".csv"
}

func safeCSVFilenamePart(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	var safe strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			safe.WriteRune(r)
		} else {
			safe.WriteByte('-')
		}
	}
	result := strings.Trim(safe.String(), "-")
	if result == "" {
		return fallback
	}
	return result
}

func curCSVExportGenerationParameters(request persistence.CURCSVExportRequest, result persistence.CURCSVExportResult) map[string]string {
	visibilityScope, visibilityAccountID := curCSVExportVisibilityScope(request.Visibility)
	parameters := csvExportGenerationParameters(request, result)
	parameters["visibility_scope"] = visibilityScope
	parameters["visibility_account_id"] = visibilityAccountID
	return parameters
}

func focusCSVExportGenerationParameters(request persistence.CURCSVExportRequest, result persistence.CURCSVExportResult) map[string]string {
	parameters := curCSVExportGenerationParameters(request, result)
	parameters["schema"] = "FOCUS-like"
	parameters["schema_version"] = "2026-06-09"
	return parameters
}

func csvExportGenerationParameters(request persistence.CURCSVExportRequest, result persistence.CURCSVExportResult) map[string]string {
	parameters := map[string]string{
		"billing_period_start": request.BillingPeriodStart,
		"billing_period_end":   request.BillingPeriodEnd,
		"payer_account_id":     request.PayerAccountID,
		"usage_account_id":     request.UsageAccountID,
		"line_item_status":     request.LineItemStatus,
		"generated_at":         result.GeneratedAt,
		"source_bill_id":       result.SourceBillID,
		"rows_written":         strconv.Itoa(result.RowsWritten),
	}
	if request.Limit > 0 {
		parameters["limit"] = strconv.Itoa(request.Limit)
	}
	return parameters
}

const (
	exportVisibilityScopeAllAccounts   = "all-accounts"
	exportVisibilityScopePayerAccount  = "payer-account"
	exportVisibilityScopeUsageAccount  = "usage-account"
	exportVisibilityScopeKey           = "visibility_scope"
	exportVisibilityAccountIDParameter = "visibility_account_id"
)

// curCSVExportVisibilityScope serializes the policy row scope used when producing stored export bytes.
func curCSVExportVisibilityScope(visibility persistence.BillingVisibilityFilter) (string, string) {
	usageAccountID := strings.TrimSpace(visibility.UsageAccountID)
	if usageAccountID != "" {
		return exportVisibilityScopeUsageAccount, usageAccountID
	}
	payerAccountID := strings.TrimSpace(visibility.PayerAccountID)
	if payerAccountID != "" {
		return exportVisibilityScopePayerAccount, payerAccountID
	}
	return exportVisibilityScopeAllAccounts, ""
}

// curCSVExportVisibilityFromGenerationParameters restores the row scope needed to regenerate a stored export.
func curCSVExportVisibilityFromGenerationParameters(parameters map[string]string) (persistence.BillingVisibilityFilter, error) {
	scope := strings.TrimSpace(parameters[exportVisibilityScopeKey])
	accountID := strings.TrimSpace(parameters[exportVisibilityAccountIDParameter])
	switch scope {
	case "":
		return persistence.BillingVisibilityFilter{}, nil
	case exportVisibilityScopeAllAccounts:
		return persistence.BillingVisibilityFilter{}, nil
	case exportVisibilityScopePayerAccount:
		if accountID == "" {
			return persistence.BillingVisibilityFilter{}, fmt.Errorf("stored export payer visibility account ID is required")
		}
		return persistence.BillingVisibilityFilter{PayerAccountID: accountID}, nil
	case exportVisibilityScopeUsageAccount:
		if accountID == "" {
			return persistence.BillingVisibilityFilter{}, fmt.Errorf("stored export usage visibility account ID is required")
		}
		return persistence.BillingVisibilityFilter{UsageAccountID: accountID}, nil
	default:
		return persistence.BillingVisibilityFilter{}, fmt.Errorf("stored export visibility scope %q is unsupported", scope)
	}
}

// exportFileVisibilityScope reads the stored generation scope used for member export authorization.
func exportFileVisibilityScope(file persistence.ExportFile) (string, string, error) {
	visibility, err := curCSVExportVisibilityFromGenerationParameters(file.GenerationParameters)
	if err != nil {
		return "", "", err
	}
	scope, accountID := curCSVExportVisibilityScope(visibility)
	if strings.TrimSpace(file.GenerationParameters[exportVisibilityScopeKey]) == "" {
		scope = ""
		accountID = ""
	}
	return scope, accountID, nil
}

func exportReconciliationReportViewFromReport(report persistence.CURExportReconciliationReport, viewer exportViewerFields) exportReconciliationReportView {
	usageAccountID := strings.TrimSpace(report.UsageAccountID)
	if usageAccountID == "" {
		usageAccountID = "all accounts"
	}
	lineItemStatus := strings.TrimSpace(report.LineItemStatus)
	if lineItemStatus == "" {
		lineItemStatus = "all statuses"
	}
	view := exportReconciliationReportView{
		Period:         report.BillingPeriodStart + " to " + report.BillingPeriodEnd,
		PayerAccountID: report.PayerAccountID,
		UsageAccountID: usageAccountID,
		LineItemStatus: lineItemStatus,
		CurrencyCode:   report.CurrencyCode,
		Status:         displayBillState(report.Status),
		Flags:          strings.Join(report.Flags, ", "),
		CURCSVPath: curCSVExportPathWithViewer(persistence.CURCSVExportRequest{
			BillingPeriodStart: report.BillingPeriodStart,
			BillingPeriodEnd:   report.BillingPeriodEnd,
			PayerAccountID:     report.PayerAccountID,
			UsageAccountID:     report.UsageAccountID,
			LineItemStatus:     report.LineItemStatus,
			Limit:              report.Limit,
		}, viewer),
	}
	view.DocumentRows = []exportReconciliationDocumentRowView{
		{
			Source:         "Export",
			ID:             "CUR-like CSV",
			Status:         lineItemStatus,
			LineItemCount:  report.ExportLineItemCount,
			Charges:        formatUSDMicros(report.ExportChargeMicros),
			Credits:        formatUSDMicros(report.ExportCreditMicros),
			Refunds:        formatUSDMicros(report.ExportRefundMicros),
			Tax:            formatUSDMicros(report.ExportTaxMicros),
			Total:          formatUSDMicros(report.ExportTotalMicros),
			ItemResidual:   "0",
			ChargeResidual: formatUSDMicros(0),
			CreditResidual: formatUSDMicros(0),
			RefundResidual: formatUSDMicros(0),
			TaxResidual:    formatUSDMicros(0),
			TotalResidual:  formatUSDMicros(0),
		},
		{
			Source:         "Bill",
			ID:             displayOptionalValue(report.BillID),
			Status:         displayBillState(report.BillState),
			LineItemCount:  report.BillLineItemCount,
			Charges:        formatUSDMicros(report.BillChargeMicros),
			Credits:        formatUSDMicros(report.BillCreditMicros),
			Refunds:        formatUSDMicros(report.BillRefundMicros),
			Tax:            formatUSDMicros(report.BillTaxMicros),
			Total:          formatUSDMicros(report.BillTotalMicros),
			ItemResidual:   strconv.Itoa(report.BillLineItemResidual),
			ChargeResidual: formatUSDMicros(report.BillChargeResidualMicros),
			CreditResidual: formatUSDMicros(report.BillCreditResidualMicros),
			RefundResidual: formatUSDMicros(report.BillRefundResidualMicros),
			TaxResidual:    formatUSDMicros(report.BillTaxResidualMicros),
			TotalResidual:  formatUSDMicros(report.BillTotalResidualMicros),
		},
		{
			Source:         "Invoice",
			ID:             displayOptionalValue(report.InvoiceID),
			Status:         displayBillState(report.InvoiceStatus),
			LineItemCount:  report.InvoiceLineItemCount,
			Charges:        formatUSDMicros(report.InvoiceChargeMicros),
			Credits:        formatUSDMicros(report.InvoiceCreditMicros),
			Refunds:        formatUSDMicros(report.InvoiceRefundMicros),
			Tax:            formatUSDMicros(report.InvoiceTaxMicros),
			Total:          formatUSDMicros(report.InvoiceTotalMicros),
			ItemResidual:   strconv.Itoa(report.InvoiceLineItemResidual),
			ChargeResidual: formatUSDMicros(report.InvoiceChargeResidualMicros),
			CreditResidual: formatUSDMicros(report.InvoiceCreditResidualMicros),
			RefundResidual: formatUSDMicros(report.InvoiceRefundResidualMicros),
			TaxResidual:    formatUSDMicros(report.InvoiceTaxResidualMicros),
			TotalResidual:  formatUSDMicros(report.InvoiceTotalResidualMicros),
		},
	}
	return view
}
