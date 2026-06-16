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
	QueryLabPath       string
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
		h.renderExportsForValues(w, r, exportHTTPStatus(err), "regenerate export: "+err.Error(), "", r.PostForm)
		return
	}
	file, err := h.exportFiles.GetByFilename(r.Context(), filename)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		h.renderExportsForValues(w, r, status, "regenerate export: "+err.Error(), "", r.PostForm)
		return
	}
	if err := ensureExportFileVisibleToPolicy(policy, file); err != nil {
		h.renderExportsForValues(w, r, http.StatusForbidden, "regenerate export: "+err.Error(), "", r.PostForm)
		return
	}
	record, result, err := h.regenerateExportFile(r.Context(), file, policy)
	if err != nil {
		h.renderExportsForValues(w, r, exportGenerationHTTPStatus(err), "regenerate export: "+err.Error(), "", r.PostForm)
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
		h.renderExportsForValues(w, r, http.StatusBadRequest, "generate CUR export: "+err.Error(), "", r.PostForm)
		return
	}
	policy, err := h.exportPolicyFromValues(r.Context(), r.PostForm)
	if err != nil {
		h.renderExportsForValues(w, r, exportHTTPStatus(err), "generate CUR export: "+err.Error(), "", r.PostForm)
		return
	}
	request, err = h.scopedCURCSVExportRequest(r.Context(), request, policy)
	if err != nil {
		h.renderExportsForValues(w, r, exportHTTPStatus(err), "generate CUR export: "+err.Error(), "", r.PostForm)
		return
	}

	record, result, err := h.persistCURCSVExportFile(r.Context(), request)
	if err != nil {
		h.renderExportsForValues(w, r, exportGenerationHTTPStatus(err), "generate CUR export: "+err.Error(), "", r.PostForm)
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
		h.renderExportsForValues(w, r, http.StatusBadRequest, "generate FOCUS export: "+err.Error(), "", r.PostForm)
		return
	}
	policy, err := h.exportPolicyFromValues(r.Context(), r.PostForm)
	if err != nil {
		h.renderExportsForValues(w, r, exportHTTPStatus(err), "generate FOCUS export: "+err.Error(), "", r.PostForm)
		return
	}
	request, err = h.scopedCURCSVExportRequest(r.Context(), request, policy)
	if err != nil {
		h.renderExportsForValues(w, r, exportHTTPStatus(err), "generate FOCUS export: "+err.Error(), "", r.PostForm)
		return
	}

	record, result, err := h.persistFOCUSCSVExportFile(r.Context(), request)
	if err != nil {
		h.renderExportsForValues(w, r, exportGenerationHTTPStatus(err), "generate FOCUS export: "+err.Error(), "", r.PostForm)
		return
	}

	flash := fmt.Sprintf("Generated %s from %d source rows", record.Filename, result.RowsWritten)
	http.Redirect(w, r, exportsPathWithViewer(exportViewerFieldsFromValues(r.PostForm), flash), http.StatusSeeOther)
}

func (h exportsHandler) renderExports(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	h.renderExportsForValues(w, r, status, errorMessage, flashMessage, r.URL.Query())
}

func (h exportsHandler) renderExportsForValues(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string, values url.Values) {
	viewer := exportViewerFieldsFromValues(values)
	data := exportsPageData{
		WorkspaceReady:      h.db != nil,
		Error:               errorMessage,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Actions:             uiActionBar(uiActionLink("Query Lab", queryLabPath()), uiActionLink("Reconciliation", curExportReconciliationPathWithViewer(persistence.CURExportReconciliationRequest{}, viewer)), uiActionLink("Bills", billsPathWithExportViewer(viewer))),
		Filters:             exportFileFilterFromValues(values),
		GenerateCURCSV:      curCSVGenerationFormFromValues(values),
		GenerateFOCUSCSV:    focusCSVGenerationFormFromValues(values),
		Tables:              exportsTablesView{Files: exportFilesTable()},
	}
	if h.db != nil && data.Error == "" {
		request, err := exportFileListRequestFromFilter(data.Filters)
		if err != nil {
			status = http.StatusBadRequest
			data.Error = "list exports: " + err.Error()
		} else {
			policy, err := h.exportPolicyFromValues(r.Context(), values)
			if err != nil {
				status = exportHTTPStatus(err)
				data.Error = "list exports: " + err.Error()
				data.Notices = uiNotices(flashMessage, data.Error)
				renderPage(w, status, pageLayoutOptions{
					Title:     "Exports - Billing Simulator",
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
					Title:     "Exports - Billing Simulator",
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
		Title:     "Exports - Billing Simulator",
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
		Actions:             uiActionBar(uiActionLink("Query Lab", queryLabPath()), uiActionLink("Bills", billsPathWithExportViewer(viewer))),
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
					Title:     "Export Reconciliation - Billing Simulator",
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
					Title:     "Export Reconciliation - Billing Simulator",
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
					uiActionLink("Query Lab", queryLabPath()),
					uiActionLink("Bills", billsPathWithExportViewer(viewer)),
				)
			}
		}
	}
	data.Notices = uiNotices("", data.Error)

	renderPage(w, status, pageLayoutOptions{
		Title:     "Export Reconciliation - Billing Simulator",
		ActiveNav: "exports",
	}, exportReconciliationPageTemplate, data, "render export reconciliation page")
}
