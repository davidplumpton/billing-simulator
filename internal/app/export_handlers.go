package app

import (
	"bytes"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

type exportsHandler struct {
	db  *sql.DB
	cur persistence.CURLineItemRepository
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
	LineItemStatus      string
	Limit               string
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

func newExportsHandler(db *sql.DB) exportsHandler {
	return exportsHandler{
		db:  db,
		cur: persistence.NewCURLineItemRepository(db),
	}
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

	var body bytes.Buffer
	if _, err := h.cur.WriteCSVExport(r.Context(), &body, request); err != nil {
		http.Error(w, "export CUR CSV: "+err.Error(), http.StatusBadRequest)
		return
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

	data := exportReconciliationPageData{
		WorkspaceReady:      h.db != nil,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Actions:             uiActionBar(uiActionLink("Bills", "/bills")),
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
			report, err := h.cur.GetReconciliationReport(r.Context(), request)
			if err != nil {
				status = http.StatusBadRequest
				data.Error = "reconcile CUR export: " + err.Error()
			} else {
				data.Loaded = true
				data.Report = exportReconciliationReportViewFromReport(report)
				data.Actions = uiActionBar(
					uiActionLink("CUR CSV", data.Report.CURCSVPath),
					uiActionLink("Bills", "/bills"),
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

func exportReconciliationFilterFromRequest(r *http.Request) exportReconciliationFilterView {
	query := r.URL.Query()
	filter := exportReconciliationFilterView{
		BillingPeriodStart: query.Get("billing_period_start"),
		BillingPeriodEnd:   query.Get("billing_period_end"),
		PayerAccountID:     query.Get("payer_account_id"),
		UsageAccountID:     query.Get("usage_account_id"),
		LineItemStatus:     query.Get("line_item_status"),
		Limit:              query.Get("limit"),
		ApplyButton:        uiSubmitButton("Run Report"),
		ClearPath:          "/exports/reconciliation",
	}
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
	parts := []string{
		"cur",
		safeCSVFilenamePart(request.BillingPeriodStart, "period-start"),
		safeCSVFilenamePart(request.BillingPeriodEnd, "period-end"),
		safeCSVFilenamePart(request.PayerAccountID, "payer"),
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
	values := url.Values{}
	appendQueryValue(values, "billing_period_start", request.BillingPeriodStart)
	appendQueryValue(values, "billing_period_end", request.BillingPeriodEnd)
	appendQueryValue(values, "payer_account_id", request.PayerAccountID)
	appendQueryValue(values, "usage_account_id", request.UsageAccountID)
	appendQueryValue(values, "line_item_status", request.LineItemStatus)
	if request.Limit > 0 {
		values.Set("limit", strconv.Itoa(request.Limit))
	}
	return "/exports/cur.csv?" + values.Encode()
}

func curExportReconciliationPath(request persistence.CURExportReconciliationRequest) string {
	values := url.Values{}
	appendQueryValue(values, "billing_period_start", request.BillingPeriodStart)
	appendQueryValue(values, "billing_period_end", request.BillingPeriodEnd)
	appendQueryValue(values, "payer_account_id", request.PayerAccountID)
	appendQueryValue(values, "usage_account_id", request.UsageAccountID)
	appendQueryValue(values, "line_item_status", request.LineItemStatus)
	if request.Limit > 0 {
		values.Set("limit", strconv.Itoa(request.Limit))
	}
	return "/exports/reconciliation?" + values.Encode()
}

func appendQueryValue(values url.Values, key, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		values.Set(key, value)
	}
}

func exportReconciliationReportViewFromReport(report persistence.CURExportReconciliationReport) exportReconciliationReportView {
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
		CURCSVPath: curCSVExportPath(persistence.CURCSVExportRequest{
			BillingPeriodStart: report.BillingPeriodStart,
			BillingPeriodEnd:   report.BillingPeriodEnd,
			PayerAccountID:     report.PayerAccountID,
			UsageAccountID:     report.UsageAccountID,
			LineItemStatus:     report.LineItemStatus,
		}),
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
