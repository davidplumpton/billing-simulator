package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/billingvisibility"
	"aws-billing-simulator/internal/persistence"
)

type exportsHandler struct {
	db            *sql.DB
	workspacePath string
	cur           persistence.CURLineItemRepository
	exportFiles   persistence.ExportFileRepository
}

type exportsPageData struct {
	WorkspaceReady      bool
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	Actions             uiActionBarView
	Filters             exportFileFilterView
	Files               []exportFileRowView
	Tables              exportsTablesView
}

type exportFileFilterView struct {
	ExportType         string
	BillingPeriodStart string
	BillingPeriodEnd   string
	PayerAccountID     string
	UsageAccountID     string
	ViewerRole         string
	ViewerAccountID    string
	Limit              string
	ExportTypeField    uiSelectFieldView
	ViewerRoleField    uiSelectFieldView
	ViewerAccountField uiInputFieldView
	ApplyButton        uiSubmitButtonView
	ClearPath          string
	HasFilters         bool
}

type exportFileRowView struct {
	Filename           string
	ExportType         string
	Period             string
	PayerAccountID     string
	UsageAccountID     string
	LineItemStatus     string
	Size               string
	Checksum           string
	GeneratedAt        string
	SourceBillID       string
	RowsWritten        string
	CreatedAt          string
	UpdatedAt          string
	DownloadPath       string
	RegenerateFilename string
	ViewerRole         string
	ViewerAccountID    string
	CanRegenerate      bool
	ReconciliationPath string
}

type exportsTablesView struct {
	Files uiTableView
}

type exportReconciliationPageData struct {
	WorkspaceReady      bool
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	Actions             uiActionBarView
	Filters             exportReconciliationFilterView
	Loaded              bool
	Report              exportReconciliationReportView
	Tables              exportReconciliationTablesView
}

type exportReconciliationFilterView struct {
	BillingPeriodStart  string
	BillingPeriodEnd    string
	PayerAccountID      string
	UsageAccountID      string
	ViewerRole          string
	ViewerAccountID     string
	LineItemStatus      string
	Limit               string
	ViewerRoleField     uiSelectFieldView
	ViewerAccountField  uiInputFieldView
	LineItemStatusField uiSelectFieldView
	ApplyButton         uiSubmitButtonView
	ClearPath           string
	HasFilters          bool
}

type exportReconciliationReportView struct {
	Period         string
	PayerAccountID string
	UsageAccountID string
	LineItemStatus string
	CurrencyCode   string
	Status         string
	Flags          string
	CURCSVPath     string
	DocumentRows   []exportReconciliationDocumentRowView
}

type exportReconciliationDocumentRowView struct {
	Source         string
	ID             string
	Status         string
	LineItemCount  int
	Charges        string
	Credits        string
	Refunds        string
	Tax            string
	Total          string
	ItemResidual   string
	ChargeResidual string
	CreditResidual string
	RefundResidual string
	TaxResidual    string
	TotalResidual  string
}

type exportReconciliationTablesView struct {
	Documents uiTableView
}

type exportAccessError struct {
	err error
}

func (e exportAccessError) Error() string {
	return e.err.Error()
}

func (e exportAccessError) Unwrap() error {
	return e.err
}

func newExportsHandler(db *sql.DB) exportsHandler {
	return newWorkspaceExportsHandler(db, "")
}

func newWorkspaceExportsHandler(db *sql.DB, workspacePath string) exportsHandler {
	return exportsHandler{
		db:            db,
		workspacePath: strings.TrimSpace(workspacePath),
		cur:           persistence.NewCURLineItemRepository(db),
		exportFiles:   persistence.NewExportFileRepository(db, workspacePath),
	}
}

// handleExports renders the generated export file inventory for the current workspace.
func (h exportsHandler) handleExports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	h.renderExports(w, r, http.StatusOK, "", flashFromQuery(r))
}

// handleExportFileDownload serves one generated export from the workspace exports directory.
func (h exportsHandler) handleExportFileDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		http.Error(w, "workspace required", http.StatusConflict)
		return
	}

	filename, ok := exportFileDownloadFilenameFromPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	policy, err := h.exportPolicyFromValues(r.Context(), r.URL.Query())
	if err != nil {
		http.Error(w, "download export file: "+err.Error(), exportHTTPStatus(err))
		return
	}
	record, err := h.exportFiles.GetByFilename(r.Context(), filename)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "download export file: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := ensureExportFileVisibleToPolicy(policy, record); err != nil {
		http.Error(w, "download export file: "+err.Error(), http.StatusForbidden)
		return
	}
	record, content, err := h.exportFiles.Read(r.Context(), filename)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "download export file: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", exportFileContentType(record.ExportType))
	w.Header().Set("Content-Disposition", `attachment; filename="`+record.Filename+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(record.SizeBytes, 10))
	w.Header().Set("X-Simulator-Export-Filename", record.Filename)
	w.Header().Set("X-Simulator-Export-Checksum", record.ChecksumSHA256)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(content)
	}
}

// handleRegenerateExport rewrites a stored export from its recorded generation parameters.
func (h exportsHandler) handleRegenerateExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		http.Error(w, "workspace required", http.StatusConflict)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderExports(w, r, http.StatusBadRequest, "regenerate export: "+err.Error(), "")
		return
	}

	filename := strings.TrimSpace(r.PostForm.Get("filename"))
	policy, err := h.exportPolicyFromValues(r.Context(), r.PostForm)
	if err != nil {
		h.renderExports(w, r, exportHTTPStatus(err), "regenerate export: "+err.Error(), "")
		return
	}
	file, err := h.exportFiles.GetByFilename(r.Context(), filename)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		h.renderExports(w, r, status, "regenerate export: "+err.Error(), "")
		return
	}
	if err := ensureExportFileVisibleToPolicy(policy, file); err != nil {
		h.renderExports(w, r, http.StatusForbidden, "regenerate export: "+err.Error(), "")
		return
	}
	request, err := curCSVExportRequestFromExportFile(file)
	if err != nil {
		h.renderExports(w, r, http.StatusBadRequest, "regenerate export: "+err.Error(), "")
		return
	}
	request, err = h.scopedCURCSVExportRequest(r.Context(), request, policy)
	if err != nil {
		h.renderExports(w, r, exportHTTPStatus(err), "regenerate export: "+err.Error(), "")
		return
	}

	var body bytes.Buffer
	result, err := h.cur.WriteCSVExport(r.Context(), &body, request)
	if err != nil {
		h.renderExports(w, r, http.StatusBadRequest, "regenerate export: "+err.Error(), "")
		return
	}
	record, err := h.writeCURCSVExportFile(r.Context(), request, body.Bytes(), result)
	if err != nil {
		h.renderExports(w, r, http.StatusInternalServerError, "regenerate export: "+err.Error(), "")
		return
	}

	flash := fmt.Sprintf("Regenerated %s from %d source rows", record.Filename, result.RowsWritten)
	http.Redirect(w, r, exportsPathWithViewer(exportViewerFieldsFromValues(r.PostForm), flash), http.StatusSeeOther)
}

func (h exportsHandler) renderExports(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	viewer := exportViewerFieldsFromValues(r.URL.Query())
	data := exportsPageData{
		WorkspaceReady:      h.db != nil,
		Error:               errorMessage,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Actions:             uiActionBar(uiActionLink("Reconciliation", curExportReconciliationPathWithViewer(persistence.CURExportReconciliationRequest{}, viewer)), uiActionLink("Bills", billsPathWithExportViewer(viewer))),
		Filters:             exportFileFilterFromRequest(r),
		Tables:              exportsTablesView{Files: exportFilesTable()},
	}
	if h.db != nil && data.Error == "" {
		request, err := exportFileListRequestFromFilter(data.Filters)
		if err != nil {
			status = http.StatusBadRequest
			data.Error = "list exports: " + err.Error()
		} else {
			policy, err := h.exportPolicyFromValues(r.Context(), r.URL.Query())
			if err != nil {
				status = exportHTTPStatus(err)
				data.Error = "list exports: " + err.Error()
				data.Notices = uiNotices(flashMessage, data.Error)
				renderPage(w, status, pageLayoutOptions{
					Title:     "Exports - AWS Billing Simulator",
					ActiveNav: "exports",
				}, exportsPageTemplate, data, "render exports page")
				return
			}
			request, err = h.scopedExportFileListRequest(r.Context(), request, policy)
			if err != nil {
				status = exportHTTPStatus(err)
				data.Error = "list exports: " + err.Error()
				data.Notices = uiNotices(flashMessage, data.Error)
				renderPage(w, status, pageLayoutOptions{
					Title:     "Exports - AWS Billing Simulator",
					ActiveNav: "exports",
				}, exportsPageTemplate, data, "render exports page")
				return
			}
			files, err := h.exportFiles.List(r.Context(), request)
			if err != nil {
				status = http.StatusInternalServerError
				data.Error = "list exports: " + err.Error()
			} else {
				data.Files = exportFileRowsFromFiles(files, viewer)
			}
		}
	}
	data.Notices = uiNotices(flashMessage, data.Error)

	renderPage(w, status, pageLayoutOptions{
		Title:     "Exports - AWS Billing Simulator",
		ActiveNav: "exports",
	}, exportsPageTemplate, data, "render exports page")
}

// handleCURCSV exports payer-period bill line items in the simulator's CUR-like CSV schema.
func (h exportsHandler) handleCURCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		http.Error(w, "workspace required", http.StatusConflict)
		return
	}

	request, err := curCSVExportRequestFromQuery(r)
	if err != nil {
		http.Error(w, "export CUR CSV: "+err.Error(), http.StatusBadRequest)
		return
	}
	policy, err := h.exportPolicyFromValues(r.Context(), r.URL.Query())
	if err != nil {
		http.Error(w, "export CUR CSV: "+err.Error(), exportHTTPStatus(err))
		return
	}
	request, err = h.scopedCURCSVExportRequest(r.Context(), request, policy)
	if err != nil {
		http.Error(w, "export CUR CSV: "+err.Error(), exportHTTPStatus(err))
		return
	}

	var body bytes.Buffer
	result, err := h.cur.WriteCSVExport(r.Context(), &body, request)
	if err != nil {
		http.Error(w, "export CUR CSV: "+err.Error(), http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet && h.workspacePath != "" {
		record, err := h.writeCURCSVExportFile(r.Context(), request, body.Bytes(), result)
		if err != nil {
			http.Error(w, "store CUR CSV export: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Simulator-Export-Filename", record.Filename)
		w.Header().Set("X-Simulator-Export-Checksum", record.ChecksumSHA256)
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+curCSVExportFilename(request)+`"`)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body.Bytes())
	}
}

// handleCURReconciliation renders a payer-period reconciliation report for CUR-like export rows.
func (h exportsHandler) handleCURReconciliation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}

	viewer := exportViewerFieldsFromValues(r.URL.Query())
	data := exportReconciliationPageData{
		WorkspaceReady:      h.db != nil,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Actions:             uiActionBar(uiActionLink("Bills", billsPathWithExportViewer(viewer))),
		Filters:             exportReconciliationFilterFromRequest(r),
		Tables: exportReconciliationTablesView{
			Documents: uiTable(uiTableHeaders("Source", "ID", "Status", "Items", "Charges", "Credits", "Refunds", "Tax", "Total", "Item Delta", "Charge Delta", "Credit Delta", "Refund Delta", "Tax Delta", "Total Delta"), "Run a reconciliation report"),
		},
	}
	status := http.StatusOK
	if h.db != nil && data.Filters.HasFilters {
		request, err := curExportReconciliationRequestFromQuery(r)
		if err != nil {
			status = http.StatusBadRequest
			data.Error = "reconcile CUR export: " + err.Error()
		} else {
			policy, err := h.exportPolicyFromValues(r.Context(), r.URL.Query())
			if err != nil {
				status = exportHTTPStatus(err)
				data.Error = "reconcile CUR export: " + err.Error()
				data.Notices = uiNotices("", data.Error)
				renderPage(w, status, pageLayoutOptions{
					Title:     "Export Reconciliation - AWS Billing Simulator",
					ActiveNav: "exports",
				}, exportReconciliationPageTemplate, data, "render export reconciliation page")
				return
			}
			request, err = h.scopedCURExportReconciliationRequest(r.Context(), request, policy)
			if err != nil {
				status = exportHTTPStatus(err)
				data.Error = "reconcile CUR export: " + err.Error()
				data.Notices = uiNotices("", data.Error)
				renderPage(w, status, pageLayoutOptions{
					Title:     "Export Reconciliation - AWS Billing Simulator",
					ActiveNav: "exports",
				}, exportReconciliationPageTemplate, data, "render export reconciliation page")
				return
			}
			report, err := h.cur.GetReconciliationReport(r.Context(), request)
			if err != nil {
				status = http.StatusBadRequest
				data.Error = "reconcile CUR export: " + err.Error()
			} else {
				data.Loaded = true
				data.Report = exportReconciliationReportViewFromReport(report, viewer)
				data.Actions = uiActionBar(
					uiActionLink("CUR CSV", data.Report.CURCSVPath),
					uiActionLink("Bills", billsPathWithExportViewer(viewer)),
				)
			}
		}
	}
	data.Notices = uiNotices("", data.Error)

	renderPage(w, status, pageLayoutOptions{
		Title:     "Export Reconciliation - AWS Billing Simulator",
		ActiveNav: "exports",
	}, exportReconciliationPageTemplate, data, "render export reconciliation page")
}

func curCSVExportRequestFromQuery(r *http.Request) (persistence.CURCSVExportRequest, error) {
	query := r.URL.Query()
	request := persistence.CURCSVExportRequest{
		BillingPeriodStart: query.Get("billing_period_start"),
		BillingPeriodEnd:   query.Get("billing_period_end"),
		PayerAccountID:     query.Get("payer_account_id"),
		UsageAccountID:     query.Get("usage_account_id"),
		LineItemStatus:     query.Get("line_item_status"),
	}
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
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

func (h exportsHandler) exportPolicyFromValues(ctx context.Context, values url.Values) (billingvisibility.Policy, error) {
	viewer := exportViewerFieldsFromValues(values)
	if viewer.Role == "" && viewer.AccountID != "" {
		return billingvisibility.Policy{}, fmt.Errorf("viewer role is required when viewer account ID is set")
	}
	roleValue := viewer.Role
	if roleValue == "" {
		roleValue = billingvisibility.RoleManagementAccount.String()
	}
	role, err := billingvisibility.ParseRole(roleValue)
	if err != nil {
		return billingvisibility.Policy{}, err
	}
	managementAccountID, err := defaultBillingPayerAccountID(ctx, h.db, "")
	if err != nil {
		return billingvisibility.Policy{}, err
	}
	accountID := viewer.AccountID
	if (role == billingvisibility.RoleManagementAccount || role == billingvisibility.RoleFinance) && accountID == "" {
		accountID = managementAccountID
	}
	policy, err := billingvisibility.PolicyForViewer(billingvisibility.Viewer{
		Role:                role,
		AccountID:           accountID,
		ManagementAccountID: managementAccountID,
	})
	if err != nil {
		return billingvisibility.Policy{}, err
	}
	if !policy.AllowsView(billingvisibility.ViewExports) {
		return billingvisibility.Policy{}, exportAccessError{err: fmt.Errorf("billing role %q cannot view exports", policy.Role)}
	}
	return policy, nil
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
	if payerAccountID, ok := policy.PayerAccountFilter(); ok {
		if request.PayerAccountID != "" && request.PayerAccountID != payerAccountID {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export payer account %q", policy.Role, request.PayerAccountID)}
		}
		request.PayerAccountID = payerAccountID
		request.Visibility = persistence.BillingVisibilityFilter{PayerAccountID: payerAccountID}
	}
	if usageAccountID, ok := policy.UsageAccountFilter(); ok {
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
	filter.ViewerRoleField = exportsViewerRoleSelect(filter.ViewerRole)
	filter.ViewerAccountField = uiInputField("Viewer Account ID", "viewer_account_id", filter.ViewerAccountID, false)
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

func exportsViewerRoleSelect(selected string) uiSelectFieldView {
	options := []uiSelectOptionView{
		{Value: "", Label: "Default viewer"},
		{Value: billingvisibility.RoleManagementAccount.String(), Label: "Management"},
		{Value: billingvisibility.RoleFinance.String(), Label: "Finance"},
		{Value: billingvisibility.RoleMemberAccount.String(), Label: "Member"},
		{Value: billingvisibility.RoleInstructor.String(), Label: "Instructor"},
	}
	for idx := range options {
		options[idx].Selected = options[idx].Value == selected
	}
	return uiSelectFieldView{
		Label:   "Viewer Role",
		Name:    "viewer_role",
		Options: options,
	}
}

type exportViewerFields struct {
	Role      string
	AccountID string
}

func exportViewerFieldsFromValues(values url.Values) exportViewerFields {
	return exportViewerFields{
		Role:      strings.TrimSpace(values.Get("viewer_role")),
		AccountID: strings.TrimSpace(values.Get("viewer_account_id")),
	}
}

func exportViewerFieldsFromBillsFilter(filter billsFilterView) exportViewerFields {
	return exportViewerFields{
		Role:      strings.TrimSpace(filter.ViewerRole),
		AccountID: strings.TrimSpace(filter.ViewerAccountID),
	}
}

func (v exportViewerFields) appendToValues(values url.Values) {
	if v.Role != "" {
		values.Set("viewer_role", v.Role)
	}
	if v.AccountID != "" {
		values.Set("viewer_account_id", v.AccountID)
	}
}

func exportsPathWithViewer(viewer exportViewerFields, flash string) string {
	values := url.Values{}
	viewer.appendToValues(values)
	appendQueryValue(values, "flash", flash)
	if len(values) == 0 {
		return "/exports"
	}
	return "/exports?" + values.Encode()
}

func billsPathWithExportViewer(viewer exportViewerFields) string {
	values := url.Values{}
	appendQueryValue(values, "viewer_role", viewer.Role)
	appendQueryValue(values, "viewer_account_id", viewer.AccountID)
	if len(values) == 0 {
		return "/bills"
	}
	return "/bills?" + values.Encode()
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
	}
	return row
}

func curCSVExportRequestFromExportFile(file persistence.ExportFile) (persistence.CURCSVExportRequest, error) {
	if file.ExportType != persistence.ExportFileTypeCURCSV {
		return persistence.CURCSVExportRequest{}, fmt.Errorf("export type %q cannot be regenerated as CUR CSV", file.ExportType)
	}
	request := persistence.CURCSVExportRequest{
		BillingPeriodStart: file.BillingPeriodStart,
		BillingPeriodEnd:   file.BillingPeriodEnd,
		PayerAccountID:     file.PayerAccountID,
		UsageAccountID:     file.UsageAccountID,
		LineItemStatus:     file.GenerationParameters["line_item_status"],
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

func exportFileDownloadPath(filename string) string {
	return "/exports/files/" + url.PathEscape(filename)
}

func exportFileDownloadPathWithViewer(filename string, viewer exportViewerFields) string {
	path := exportFileDownloadPath(filename)
	values := url.Values{}
	viewer.appendToValues(values)
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}

func exportFileDownloadFilenameFromPath(path string) (string, bool) {
	const prefix = "/exports/files/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	raw := strings.TrimPrefix(path, prefix)
	if raw == "" || strings.Contains(raw, "/") {
		return "", false
	}
	filename, err := url.PathUnescape(raw)
	if err != nil {
		return "", false
	}
	return filename, true
}

func exportFileContentType(exportType string) string {
	switch exportType {
	case persistence.ExportFileTypeCURCSV:
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
	filter.ViewerRoleField = exportsViewerRoleSelect(filter.ViewerRole)
	filter.ViewerAccountField = uiInputField("Viewer Account ID", "viewer_account_id", filter.ViewerAccountID, false)
	filter.LineItemStatusField = exportReconciliationLineItemStatusSelect(filter.LineItemStatus)
	filter.HasFilters = filter.BillingPeriodStart != "" ||
		filter.BillingPeriodEnd != "" ||
		filter.PayerAccountID != "" ||
		filter.UsageAccountID != "" ||
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
	limitPart := "default"
	if request.Limit > 0 {
		limitPart = strconv.Itoa(request.Limit)
	}
	parts := []string{
		"cur",
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

func curCSVExportPath(request persistence.CURCSVExportRequest) string {
	return curCSVExportPathWithViewer(request, exportViewerFields{})
}

func curCSVExportPathWithViewer(request persistence.CURCSVExportRequest, viewer exportViewerFields) string {
	values := url.Values{}
	appendQueryValue(values, "billing_period_start", request.BillingPeriodStart)
	appendQueryValue(values, "billing_period_end", request.BillingPeriodEnd)
	appendQueryValue(values, "payer_account_id", request.PayerAccountID)
	appendQueryValue(values, "usage_account_id", request.UsageAccountID)
	appendQueryValue(values, "line_item_status", request.LineItemStatus)
	if request.Limit > 0 {
		values.Set("limit", strconv.Itoa(request.Limit))
	}
	viewer.appendToValues(values)
	if len(values) == 0 {
		return "/exports/cur.csv"
	}
	return "/exports/cur.csv?" + values.Encode()
}

func curCSVExportGenerationParameters(request persistence.CURCSVExportRequest, result persistence.CURCSVExportResult) map[string]string {
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

func curExportReconciliationPath(request persistence.CURExportReconciliationRequest) string {
	return curExportReconciliationPathWithViewer(request, exportViewerFields{})
}

func curExportReconciliationPathWithViewer(request persistence.CURExportReconciliationRequest, viewer exportViewerFields) string {
	values := url.Values{}
	appendQueryValue(values, "billing_period_start", request.BillingPeriodStart)
	appendQueryValue(values, "billing_period_end", request.BillingPeriodEnd)
	appendQueryValue(values, "payer_account_id", request.PayerAccountID)
	appendQueryValue(values, "usage_account_id", request.UsageAccountID)
	appendQueryValue(values, "line_item_status", request.LineItemStatus)
	if request.Limit > 0 {
		values.Set("limit", strconv.Itoa(request.Limit))
	}
	viewer.appendToValues(values)
	if len(values) == 0 {
		return "/exports/reconciliation"
	}
	return "/exports/reconciliation?" + values.Encode()
}

func appendQueryValue(values url.Values, key, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		values.Set(key, value)
	}
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

var exportsPageTemplate = newPageTemplate("exports-page", `<div class="page-heading">
			<div>
				<h1>Exports</h1>
			</div>
			{{template "ui.action-bar" .Actions}}
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="filter-bar" aria-label="Export file filters">
				<form method="get" action="/exports" class="filter-form">
					{{template "ui.select-field" .Filters.ExportTypeField}}
					{{template "ui.select-field" .Filters.ViewerRoleField}}
					{{template "ui.input-field" .Filters.ViewerAccountField}}
					<label>Billing Period Start
						<input name="billing_period_start" value="{{.Filters.BillingPeriodStart}}" placeholder="2026-02-01">
					</label>
					<label>Billing Period End
						<input name="billing_period_end" value="{{.Filters.BillingPeriodEnd}}" placeholder="2026-03-01">
					</label>
					<label>Payer Account ID
						<input name="payer_account_id" value="{{.Filters.PayerAccountID}}">
					</label>
					<label>Usage Account ID
						<input name="usage_account_id" value="{{.Filters.UsageAccountID}}">
					</label>
					<label>Limit
						<input name="limit" value="{{.Filters.Limit}}" inputmode="numeric">
					</label>
					{{template "ui.submit-button" .Filters.ApplyButton}}
					{{if .Filters.HasFilters}}<a class="button-link secondary" href="{{.Filters.ClearPath}}">Clear</a>{{end}}
				</form>
			</section>

			<section>
				<div class="section-heading">
					<h2>Generated Exports</h2>
					<span>{{len .Files}} files, recently updated first</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table exports-table">
						{{template "ui.dense-table-head" .Tables.Files}}
						<tbody>
							{{range .Files}}
								<tr>
									<td><strong>{{.Filename}}</strong><small>created {{.CreatedAt}}</small></td>
									<td><span class="status">{{.ExportType}}</span></td>
									<td>{{.Period}}</td>
									<td><strong>payer {{.PayerAccountID}}</strong><small>usage {{.UsageAccountID}}</small><small>{{.LineItemStatus}}</small></td>
									<td><strong>bill {{.SourceBillID}}</strong><small>generated {{.GeneratedAt}}</small><small>{{.RowsWritten}} rows</small></td>
									<td>{{.Size}}</td>
									<td><code>{{.Checksum}}</code></td>
									<td>{{.UpdatedAt}}</td>
									<td class="actions-cell">
										<div class="inline-actions compact-actions">
											<a class="button-link secondary" href="{{.DownloadPath}}">Download</a>
											{{if .ReconciliationPath}}<a class="button-link secondary" href="{{.ReconciliationPath}}">Reconcile</a>{{end}}
											{{if .CanRegenerate}}
												<form method="post" action="/exports/regenerate">
													<input type="hidden" name="filename" value="{{.RegenerateFilename}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<button type="submit">Regenerate</button>
												</form>
											{{end}}
										</div>
									</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Files}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
`)

var exportReconciliationPageTemplate = newPageTemplate("export-reconciliation-page", `<div class="page-heading">
			<div>
				<h1>Export Reconciliation</h1>
			</div>
			{{template "ui.action-bar" .Actions}}
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="filter-bar" aria-label="Export reconciliation filters">
				<form method="get" action="/exports/reconciliation" class="filter-form">
					{{template "ui.select-field" .Filters.ViewerRoleField}}
					{{template "ui.input-field" .Filters.ViewerAccountField}}
					<label>Billing Period Start
						<input name="billing_period_start" value="{{.Filters.BillingPeriodStart}}" placeholder="2026-02-01">
					</label>
					<label>Billing Period End
						<input name="billing_period_end" value="{{.Filters.BillingPeriodEnd}}" placeholder="2026-03-01">
					</label>
					<label>Payer Account ID
						<input name="payer_account_id" value="{{.Filters.PayerAccountID}}">
					</label>
					<label>Usage Account ID
						<input name="usage_account_id" value="{{.Filters.UsageAccountID}}">
					</label>
					{{template "ui.select-field" .Filters.LineItemStatusField}}
					<label>Limit
						<input name="limit" value="{{.Filters.Limit}}" inputmode="numeric">
					</label>
					{{template "ui.submit-button" .Filters.ApplyButton}}
					{{if .Filters.HasFilters}}<a class="button-link secondary" href="{{.Filters.ClearPath}}">Clear</a>{{end}}
				</form>
			</section>

			{{if .Loaded}}
				<section class="clock-strip">
					<div>
						<h2>Report Status</h2>
						<strong>{{.Report.Status}}</strong>
						<small>{{.Report.Flags}}</small>
					</div>
					<div class="detail-list">
						<span>Export Selection</span>
						<strong>{{.Report.Period}}</strong>
						<small>{{.Report.CurrencyCode}} payer {{.Report.PayerAccountID}}</small>
						<small>{{.Report.UsageAccountID}} - {{.Report.LineItemStatus}}</small>
					</div>
				</section>

				<section>
					<div class="section-heading">
						<h2>Bill and Invoice Comparison</h2>
						<span>{{len .Report.DocumentRows}} sources</span>
					</div>
					<div class="table-wrap">
						<table class="dense-table">
							{{template "ui.dense-table-head" .Tables.Documents}}
							<tbody>
								{{range .Report.DocumentRows}}
									<tr>
										<td><strong>{{.Source}}</strong></td>
										<td>{{.ID}}</td>
										<td><span class="status">{{.Status}}</span></td>
										<td>{{.LineItemCount}}</td>
										<td>{{.Charges}}</td>
										<td>{{.Credits}}</td>
										<td>{{.Refunds}}</td>
										<td>{{.Tax}}</td>
										<td><strong>{{.Total}}</strong></td>
										<td>{{.ItemResidual}}</td>
										<td>{{.ChargeResidual}}</td>
										<td>{{.CreditResidual}}</td>
										<td>{{.RefundResidual}}</td>
										<td>{{.TaxResidual}}</td>
										<td><strong>{{.TotalResidual}}</strong></td>
									</tr>
								{{else}}
									{{template "ui.dense-table-empty-row" $.Tables.Documents}}
								{{end}}
							</tbody>
						</table>
					</div>
				</section>
			{{end}}
		{{end}}
`)
