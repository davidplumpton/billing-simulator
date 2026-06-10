package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
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
	cur           curExportRepository
	exportFiles   persistence.ExportFileRepository
}

type curExportRepository interface {
	WriteCSVExport(context.Context, io.Writer, persistence.CURCSVExportRequest) (persistence.CURCSVExportResult, error)
	WriteFOCUSCSVExport(context.Context, io.Writer, persistence.CURCSVExportRequest) (persistence.CURCSVExportResult, error)
	GetReconciliationReport(context.Context, persistence.CURExportReconciliationRequest) (persistence.CURExportReconciliationReport, error)
}

type exportsPageData struct {
	WorkspaceReady      bool
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	Actions             uiActionBarView
	Filters             exportFileFilterView
	GenerateCURCSV      curCSVGenerationFormView
	GenerateFOCUSCSV    curCSVGenerationFormView
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

type curCSVGenerationFormView struct {
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
	GenerateButton      uiSubmitButtonView
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

type exportStorageError struct {
	err error
}

func (e exportStorageError) Error() string {
	return e.err.Error()
}

func (e exportStorageError) Unwrap() error {
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
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	h.renderExports(w, r, http.StatusOK, "", flashFromQuery(r))
}

// handleExportFileDownload serves one generated export from the workspace exports directory.
func (h exportsHandler) handleExportFileDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
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
		methodNotAllowed(w, http.MethodPost)
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
	record, result, err := h.regenerateExportFile(r.Context(), file, policy)
	if err != nil {
		h.renderExports(w, r, exportGenerationHTTPStatus(err), "regenerate export: "+err.Error(), "")
		return
	}

	flash := fmt.Sprintf("Regenerated %s from %d source rows", record.Filename, result.RowsWritten)
	http.Redirect(w, r, exportsPathWithViewer(exportViewerFieldsFromValues(r.PostForm), flash), http.StatusSeeOther)
}

// handleGenerateCURCSVExport writes a new persisted CUR-like CSV export from explicit form input.
func (h exportsHandler) handleGenerateCURCSVExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		http.Error(w, "workspace required", http.StatusConflict)
		return
	}
	if h.workspacePath == "" {
		http.Error(w, "workspace path required", http.StatusConflict)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderExports(w, r, http.StatusBadRequest, "generate CUR export: "+err.Error(), "")
		return
	}

	request, err := curCSVExportRequestFromForm(r)
	if err != nil {
		h.renderExports(w, r, http.StatusBadRequest, "generate CUR export: "+err.Error(), "")
		return
	}
	policy, err := h.exportPolicyFromValues(r.Context(), r.PostForm)
	if err != nil {
		h.renderExports(w, r, exportHTTPStatus(err), "generate CUR export: "+err.Error(), "")
		return
	}
	request, err = h.scopedCURCSVExportRequest(r.Context(), request, policy)
	if err != nil {
		h.renderExports(w, r, exportHTTPStatus(err), "generate CUR export: "+err.Error(), "")
		return
	}

	record, result, err := h.persistCURCSVExportFile(r.Context(), request)
	if err != nil {
		h.renderExports(w, r, exportGenerationHTTPStatus(err), "generate CUR export: "+err.Error(), "")
		return
	}

	flash := fmt.Sprintf("Generated %s from %d source rows", record.Filename, result.RowsWritten)
	http.Redirect(w, r, exportsPathWithViewer(exportViewerFieldsFromValues(r.PostForm), flash), http.StatusSeeOther)
}

// handleGenerateFOCUSCSVExport writes a new persisted FOCUS-like CSV export from explicit form input.
func (h exportsHandler) handleGenerateFOCUSCSVExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		http.Error(w, "workspace required", http.StatusConflict)
		return
	}
	if h.workspacePath == "" {
		http.Error(w, "workspace path required", http.StatusConflict)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderExports(w, r, http.StatusBadRequest, "generate FOCUS export: "+err.Error(), "")
		return
	}

	request, err := curCSVExportRequestFromForm(r)
	if err != nil {
		h.renderExports(w, r, http.StatusBadRequest, "generate FOCUS export: "+err.Error(), "")
		return
	}
	policy, err := h.exportPolicyFromValues(r.Context(), r.PostForm)
	if err != nil {
		h.renderExports(w, r, exportHTTPStatus(err), "generate FOCUS export: "+err.Error(), "")
		return
	}
	request, err = h.scopedCURCSVExportRequest(r.Context(), request, policy)
	if err != nil {
		h.renderExports(w, r, exportHTTPStatus(err), "generate FOCUS export: "+err.Error(), "")
		return
	}

	record, result, err := h.persistFOCUSCSVExportFile(r.Context(), request)
	if err != nil {
		h.renderExports(w, r, exportGenerationHTTPStatus(err), "generate FOCUS export: "+err.Error(), "")
		return
	}

	flash := fmt.Sprintf("Generated %s from %d source rows", record.Filename, result.RowsWritten)
	http.Redirect(w, r, exportsPathWithViewer(exportViewerFieldsFromValues(r.PostForm), flash), http.StatusSeeOther)
}

func (h exportsHandler) renderExports(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	viewer := exportViewerFieldsFromValues(r.URL.Query())
	data := exportsPageData{
		WorkspaceReady:      h.db != nil,
		Error:               errorMessage,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Actions:             uiActionBar(uiActionLink("Query Lab", "/query-lab"), uiActionLink("Reconciliation", curExportReconciliationPathWithViewer(persistence.CURExportReconciliationRequest{}, viewer)), uiActionLink("Bills", billsPathWithExportViewer(viewer))),
		Filters:             exportFileFilterFromRequest(r),
		GenerateCURCSV:      curCSVGenerationFormFromRequest(r),
		GenerateFOCUSCSV:    focusCSVGenerationFormFromRequest(r),
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
				data.Files = exportFileRowsFromFiles(visibleExportFilesForPolicy(files, policy), viewer)
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
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
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

	filename := curCSVExportFilename(request)
	if r.Method == http.MethodHead {
		writeCSVDownloadHeaders(w, filename)
		w.WriteHeader(http.StatusOK)
		return
	}

	var body bytes.Buffer
	_, err = h.cur.WriteCSVExport(r.Context(), &body, request)
	if err != nil {
		http.Error(w, "export CUR CSV: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeCSVDownloadHeaders(w, filename)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}

// handleFOCUSCSV exports payer-period bill line items in a FOCUS-like CSV schema.
func (h exportsHandler) handleFOCUSCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	if h.db == nil {
		http.Error(w, "workspace required", http.StatusConflict)
		return
	}

	request, err := curCSVExportRequestFromQuery(r)
	if err != nil {
		http.Error(w, "export FOCUS CSV: "+err.Error(), http.StatusBadRequest)
		return
	}
	policy, err := h.exportPolicyFromValues(r.Context(), r.URL.Query())
	if err != nil {
		http.Error(w, "export FOCUS CSV: "+err.Error(), exportHTTPStatus(err))
		return
	}
	request, err = h.scopedCURCSVExportRequest(r.Context(), request, policy)
	if err != nil {
		http.Error(w, "export FOCUS CSV: "+err.Error(), exportHTTPStatus(err))
		return
	}

	filename := focusCSVExportFilename(request)
	if r.Method == http.MethodHead {
		writeCSVDownloadHeaders(w, filename)
		w.WriteHeader(http.StatusOK)
		return
	}

	var body bytes.Buffer
	_, err = h.cur.WriteFOCUSCSVExport(r.Context(), &body, request)
	if err != nil {
		http.Error(w, "export FOCUS CSV: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeCSVDownloadHeaders(w, filename)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}

// handleCURReconciliation renders a payer-period reconciliation report for CUR-like export rows.
func (h exportsHandler) handleCURReconciliation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}

	viewer := exportViewerFieldsFromValues(r.URL.Query())
	data := exportReconciliationPageData{
		WorkspaceReady:      h.db != nil,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Actions:             uiActionBar(uiActionLink("Query Lab", "/query-lab"), uiActionLink("Bills", billsPathWithExportViewer(viewer))),
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
					uiActionLink("Query Lab", "/query-lab"),
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
	form.ViewerRoleField = exportsViewerRoleSelect(form.ViewerRole)
	form.ViewerAccountField = uiInputField("Viewer Account ID", "viewer_account_id", form.ViewerAccountID, false)
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
	filter.ViewerRoleField = exportsViewerRoleSelect(filter.ViewerRole)
	filter.ViewerAccountField = uiInputField("Viewer Account ID", "viewer_account_id", filter.ViewerAccountID, false)
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

			<section class="filter-bar" aria-label="Generate CUR CSV export">
				<form method="post" action="/exports/generate-cur" class="filter-form">
					{{template "ui.select-field" .GenerateCURCSV.ViewerRoleField}}
					{{template "ui.input-field" .GenerateCURCSV.ViewerAccountField}}
					<label>Billing Period Start
						<input name="billing_period_start" value="{{.GenerateCURCSV.BillingPeriodStart}}" placeholder="2026-02-01" required>
					</label>
					<label>Billing Period End
						<input name="billing_period_end" value="{{.GenerateCURCSV.BillingPeriodEnd}}" placeholder="2026-03-01" required>
					</label>
					<label>Payer Account ID
						<input name="payer_account_id" value="{{.GenerateCURCSV.PayerAccountID}}">
					</label>
					<label>Usage Account ID
						<input name="usage_account_id" value="{{.GenerateCURCSV.UsageAccountID}}">
					</label>
					{{template "ui.select-field" .GenerateCURCSV.LineItemStatusField}}
					<label>Limit
						<input name="limit" value="{{.GenerateCURCSV.Limit}}" inputmode="numeric">
					</label>
					{{template "ui.submit-button" .GenerateCURCSV.GenerateButton}}
				</form>
			</section>

			<section class="filter-bar" aria-label="Generate FOCUS CSV export">
				<form method="post" action="/exports/generate-focus" class="filter-form">
					{{template "ui.select-field" .GenerateFOCUSCSV.ViewerRoleField}}
					{{template "ui.input-field" .GenerateFOCUSCSV.ViewerAccountField}}
					<label>Billing Period Start
						<input name="billing_period_start" value="{{.GenerateFOCUSCSV.BillingPeriodStart}}" placeholder="2026-02-01" required>
					</label>
					<label>Billing Period End
						<input name="billing_period_end" value="{{.GenerateFOCUSCSV.BillingPeriodEnd}}" placeholder="2026-03-01" required>
					</label>
					<label>Payer Account ID
						<input name="payer_account_id" value="{{.GenerateFOCUSCSV.PayerAccountID}}">
					</label>
					<label>Usage Account ID
						<input name="usage_account_id" value="{{.GenerateFOCUSCSV.UsageAccountID}}">
					</label>
					{{template "ui.select-field" .GenerateFOCUSCSV.LineItemStatusField}}
					<label>Limit
						<input name="limit" value="{{.GenerateFOCUSCSV.Limit}}" inputmode="numeric">
					</label>
					{{template "ui.submit-button" .GenerateFOCUSCSV.GenerateButton}}
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
