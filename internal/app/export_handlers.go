package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
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
