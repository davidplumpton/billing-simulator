package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"aws-billing-simulator/internal/persistence"
)

type billsHandler struct {
	db       *sql.DB
	bills    persistence.BillsRepository
	invoices persistence.InvoiceDocumentRepository
	clock    persistence.SimulatorClockRepository
}

type billsPageData struct {
	WorkspaceReady           bool
	Error                    string
	Filters                  billsFilterView
	Notices                  []uiNoticeView
	WorkspaceEmptyState      uiEmptyStateView
	ClockCurrentTime         string
	ClockBillingPeriod       string
	StateCards               []billStateCardView
	BillSummaries            []billSummaryView
	BillSummariesLabel       string
	BillReconciliations      []billReconciliationView
	BillReconciliationsLabel string
	ChargeBreakdowns         []billChargeBreakdownView
	ChargeBreakdownsLabel    string
	ResourceChargeBreakdowns []billResourceChargeBreakdownView
	ResourceBreakdownsLabel  string
	Tables                   billsTablesView
}

type billStateCardView struct {
	Key   string
	Label string
	Count int
	Total string
}

type billSummaryView struct {
	ID                string
	Period            string
	PayerAccountID    string
	State             string
	LineItemCount     int
	Charges           string
	Credits           string
	Refunds           string
	Tax               string
	Total             string
	InvoiceID         string
	InvoicePath       string
	InvoiceCSVPath    string
	InvoicePDFPath    string
	CanViewInvoice    bool
	InvoiceRestricted bool
	CURCSVPath        string
	CURReconcilePath  string
	InvoiceStatus     string
	InvoiceAmountDue  string
	InvoicePaid       string
	InvoiceDate       string
	InvoiceDueDate    string
	UpdatedAt         string
}

type billChargeBreakdownView struct {
	Period         string
	PayerAccountID string
	UsageAccountID string
	ServiceCode    string
	ServiceName    string
	RegionCode     string
	UsageType      string
	Status         string
	ResourceCount  int
	LineItemCount  int
	Charges        string
	Credits        string
	Refunds        string
	Tax            string
	Total          string
	UpdatedAt      string
}

type billResourceChargeBreakdownView struct {
	Resource       string
	ResourceID     string
	Period         string
	PayerAccountID string
	UsageAccountID string
	ServiceCode    string
	ServiceName    string
	RegionCode     string
	UsageType      string
	Status         string
	LineItemCount  int
	Charges        string
	Credits        string
	Refunds        string
	Tax            string
	Total          string
	Description    string
}

type billReconciliationView struct {
	BillID           string
	Period           string
	PayerAccountID   string
	State            string
	Status           string
	CurrencyCode     string
	BillLineItems    int
	SourceLineItems  int
	LineItemResidual int
	BillTotal        string
	SourceTotal      string
	RoundingResidual string
	ChargeResidual   string
	CreditResidual   string
	RefundResidual   string
	TaxResidual      string
	UpdatedAt        string
}

type invoicePageData struct {
	WorkspaceReady        bool
	Loaded                bool
	Error                 string
	Notices               []uiNoticeView
	WorkspaceEmptyState   uiEmptyStateView
	Actions               uiActionBarView
	InvoiceID             string
	BillID                string
	DocumentVersion       int
	DocumentStatus        string
	BillingPeriod         string
	InvoiceDate           string
	DueDate               string
	SellerOfRecord        string
	SellerAddress         string
	SellerTaxRegistration string
	PayerAccountID        string
	BillToName            string
	BillToEmail           string
	BillToAddress         string
	BillToTaxRegistration string
	CurrencyCode          string
	LineItemCount         int
	Charges               string
	Credits               string
	Refunds               string
	Tax                   string
	Total                 string
	PaymentStatus         string
	AmountDue             string
	AmountPaid            string
	CSVPath               string
	PDFPath               string
	ServiceSummaries      []invoiceChargeSummaryView
	AccountSummaries      []invoiceAccountChargeSummaryView
	LineItems             []invoiceLineItemView
	Tables                invoiceTablesView
}

type billsTablesView struct {
	BillReconciliations      uiTableView
	ChargeBreakdowns         uiTableView
	ResourceChargeBreakdowns uiTableView
	BillSummaries            uiTableView
}

type billsFilterView struct {
	PayerAccountID     string
	UsageAccountID     string
	ServiceCode        string
	ViewerRole         string
	ViewerAccountID    string
	ViewerRoleSelect   uiSelectFieldView
	ViewerAccountField uiInputFieldView
	HasFilters         bool
	ApplyButton        uiSubmitButtonView
	ClearPath          string
}

type invoiceTablesView struct {
	ServiceSummaries uiTableView
	AccountSummaries uiTableView
	LineItems        uiTableView
}

type invoiceChargeSummaryView struct {
	ServiceCode   string
	ServiceName   string
	CurrencyCode  string
	LineItemCount int
	Charges       string
	Credits       string
	Refunds       string
	Tax           string
	Total         string
}

type invoiceAccountChargeSummaryView struct {
	UsageAccountID string
	CurrencyCode   string
	LineItemCount  int
	Charges        string
	Credits        string
	Refunds        string
	Tax            string
	Total          string
}

type invoiceLineItemView struct {
	ID             string
	Resource       string
	ResourceID     string
	UsageAccountID string
	ServiceCode    string
	ServiceName    string
	LineItemType   string
	RegionCode     string
	UsageType      string
	Operation      string
	Window         string
	Quantity       string
	Rate           string
	Cost           string
	Description    string
}

type billStateDefinition struct {
	Key   string
	Label string
}

const (
	invoiceExportHTML = ""
	invoiceExportCSV  = "csv"
	invoiceExportPDF  = "pdf"

	invoiceCSVPathSuffix = "/line-items.csv"
	invoicePDFPathSuffix = "/document.pdf"
)

type invoiceRoute struct {
	InvoiceID string
	Export    string
}

// newBillsHandler builds the repositories needed for the Bills page.
func newBillsHandler(db *sql.DB) billsHandler {
	return billsHandler{
		db:       db,
		bills:    persistence.NewBillsRepository(db),
		invoices: persistence.NewInvoiceDocumentRepository(db),
		clock:    persistence.NewSimulatorClockRepository(db),
	}
}

// handleBills serves the read-only bill state view.
func (h billsHandler) handleBills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	h.renderBills(w, r, http.StatusOK, "")
}

// handleInvoice serves one printable synthetic invoice document.
func (h billsHandler) handleInvoice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	route, ok := invoiceRouteFromPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch route.Export {
	case invoiceExportCSV:
		h.handleInvoiceCSV(w, r, route.InvoiceID)
	case invoiceExportPDF:
		h.handleInvoicePDF(w, r, route.InvoiceID)
	default:
		h.renderInvoice(w, r, http.StatusOK, route.InvoiceID, "")
	}
}

// handleInvoiceIndex sends collection-level invoice requests to the bill list that exposes invoice links.
func (h billsHandler) handleInvoiceIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	target := "/bills"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// renderBills builds the dedicated bills state page from the current workspace.
func (h billsHandler) renderBills(w http.ResponseWriter, r *http.Request, status int, errorMessage string) {
	data := billsPageData{
		WorkspaceReady:      h.db != nil,
		Error:               errorMessage,
		Filters:             billsFilterFromRequest(r),
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		StateCards:          billStateCards(nil),
		Tables:              billsTables(),
	}
	if h.db != nil {
		if err := h.loadBillsPageData(r.Context(), &data); err != nil {
			status = http.StatusInternalServerError
			data.Error = err.Error()
		}
	}
	data.Notices = uiNotices("", data.Error)

	if wantsPageFragment(r, "bills") {
		renderPageFragment(w, status, billsPageTemplate, "bills.refresh", data, "render bills fragment")
		return
	}

	renderPage(w, status, pageLayoutOptions{
		Title:     "Bills - AWS Billing Simulator",
		ActiveNav: "bills",
	}, billsPageTemplate, data, "render bills page")
}

// renderInvoice builds the printable invoice page from the invoice read model.
func (h billsHandler) renderInvoice(w http.ResponseWriter, r *http.Request, status int, invoiceID, errorMessage string) {
	data := invoicePageData{
		WorkspaceReady: h.db != nil,
		InvoiceID:      invoiceID,
		Error:          errorMessage,
	}
	viewer := exportViewerFieldsFromBillsFilter(billsFilterFromRequest(r))
	if h.db != nil && data.Error == "" {
		printable, err := h.invoices.GetPrintableByInvoiceID(r.Context(), invoiceID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
				data.Error = "Invoice not found."
			} else {
				status = http.StatusInternalServerError
				data.Error = err.Error()
			}
		} else if err := h.ensureInvoiceViewerAccess(r.Context(), r, printable.Document.PayerAccountID); err != nil {
			status = http.StatusForbidden
			data.Error = err.Error()
		} else {
			data = invoicePageDataFromPrintable(printable, viewer)
		}
	}
	data.Notices = uiNotices("", data.Error)
	data.WorkspaceEmptyState = uiWorkspaceRequiredState()
	data.Actions = invoiceActionBar(data, viewer)
	data.Tables = invoiceTables()

	renderPage(w, status, pageLayoutOptions{
		Title:     "Invoice " + data.InvoiceID + " - AWS Billing Simulator",
		ActiveNav: "bills",
		MainClass: "invoice-page",
	}, invoicePageTemplate, data, "render invoice page")
}

// loadBillsPageData reads clock context and bill summaries for rendering.
func (h billsHandler) loadBillsPageData(ctx context.Context, data *billsPageData) error {
	clock, err := h.clock.Get(ctx)
	if err != nil {
		return err
	}
	data.ClockCurrentTime = clock.CurrentTime
	data.ClockBillingPeriod = fmt.Sprintf("%s to %s (%d days)", clock.BillingPeriodStart, clock.BillingPeriodEnd, clock.BillingPeriodDays)

	defaultPayerAccountID, err := defaultBillingPayerAccountID(ctx, h.db, "")
	if err != nil {
		return err
	}
	visibility, err := h.billingVisibilityFilter(ctx, data.Filters)
	if err != nil {
		return err
	}
	canViewInvoice, err := h.invoiceAccessPredicate(ctx, data.Filters)
	if err != nil {
		return err
	}
	allSummaries, err := h.bills.ListBillStateSummaries(ctx, persistence.BillStateSummaryRequest{
		AllRows:               true,
		DefaultPayerAccountID: defaultPayerAccountID,
		PayerAccountID:        data.Filters.PayerAccountID,
		Visibility:            visibility,
	})
	if err != nil {
		return err
	}
	allSummaries = filterBillStateSummaries(allSummaries, data.Filters)
	data.StateCards = billStateCards(allSummaries)

	summaries, err := h.bills.ListBillStateSummaries(ctx, persistence.BillStateSummaryRequest{
		Limit:                 50,
		DefaultPayerAccountID: defaultPayerAccountID,
		PayerAccountID:        data.Filters.PayerAccountID,
		Visibility:            visibility,
	})
	if err != nil {
		return err
	}
	summaries = filterBillStateSummaries(summaries, data.Filters)
	data.BillSummariesLabel = limitedTableLabel(len(summaries), len(allSummaries), "summary", "summaries")
	for _, summary := range summaries {
		data.BillSummaries = append(data.BillSummaries, billSummaryViewFromSummary(summary, data.Filters, canViewInvoice(summary.PayerAccountID)))
	}

	allReconciliations, err := h.bills.ListBillReconciliations(ctx, persistence.BillReconciliationRequest{
		AllRows:        true,
		PayerAccountID: data.Filters.PayerAccountID,
		Visibility:     visibility,
	})
	if err != nil {
		return err
	}
	allReconciliations = filterBillReconciliations(allReconciliations, data.Filters)
	reconciliations, err := h.bills.ListBillReconciliations(ctx, persistence.BillReconciliationRequest{
		Limit:          50,
		PayerAccountID: data.Filters.PayerAccountID,
		Visibility:     visibility,
	})
	if err != nil {
		return err
	}
	reconciliations = filterBillReconciliations(reconciliations, data.Filters)
	data.BillReconciliationsLabel = limitedTableLabel(len(reconciliations), len(allReconciliations), "bill", "bills")
	for _, reconciliation := range reconciliations {
		data.BillReconciliations = append(data.BillReconciliations, billReconciliationViewFromSummary(reconciliation))
	}

	allBreakdowns, err := h.bills.ListChargeBreakdowns(ctx, persistence.BillChargeBreakdownRequest{
		AllRows:        true,
		PayerAccountID: data.Filters.PayerAccountID,
		UsageAccountID: data.Filters.UsageAccountID,
		ServiceCode:    data.Filters.ServiceCode,
		Visibility:     visibility,
	})
	if err != nil {
		return err
	}
	allBreakdowns.Summaries = filterBillChargeSummaries(allBreakdowns.Summaries, data.Filters)
	allBreakdowns.Resources = filterBillResourceChargeSummaries(allBreakdowns.Resources, data.Filters)
	breakdowns, err := h.bills.ListChargeBreakdowns(ctx, persistence.BillChargeBreakdownRequest{
		Limit:          75,
		PayerAccountID: data.Filters.PayerAccountID,
		UsageAccountID: data.Filters.UsageAccountID,
		ServiceCode:    data.Filters.ServiceCode,
		Visibility:     visibility,
	})
	if err != nil {
		return err
	}
	breakdowns.Summaries = filterBillChargeSummaries(breakdowns.Summaries, data.Filters)
	breakdowns.Resources = filterBillResourceChargeSummaries(breakdowns.Resources, data.Filters)
	data.ChargeBreakdownsLabel = limitedTableLabel(len(breakdowns.Summaries), len(allBreakdowns.Summaries), "group", "groups")
	data.ResourceBreakdownsLabel = limitedTableLabel(len(breakdowns.Resources), len(allBreakdowns.Resources), "row", "rows")
	for _, summary := range breakdowns.Summaries {
		data.ChargeBreakdowns = append(data.ChargeBreakdowns, billChargeBreakdownViewFromSummary(summary))
	}
	for _, summary := range breakdowns.Resources {
		data.ResourceChargeBreakdowns = append(data.ResourceChargeBreakdowns, billResourceChargeBreakdownViewFromSummary(summary))
	}
	return nil
}

var billsPageTemplate = newPageTemplate("bills-page", `<div class="page-heading">
			<div>
				<h1>Bills</h1>
			</div>
		</div>

		<div id="bills-refresh" data-partial-surface="bills">
			{{template "bills.refresh" .}}
		</div>

{{define "bills.refresh"}}
		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="filter-bar" aria-label="Bill filters">
				<form method="get" action="/bills" class="filter-form" data-partial-form="bills" data-partial-target="#bills-refresh" data-partial-auto="true">
					{{template "ui.select-field" .Filters.ViewerRoleSelect}}
					{{template "ui.input-field" .Filters.ViewerAccountField}}
					<label>Payer Account ID
						<input name="payer_account_id" value="{{.Filters.PayerAccountID}}">
					</label>
					<label>Usage Account ID
						<input name="usage_account_id" value="{{.Filters.UsageAccountID}}">
					</label>
					<label>Service
						<input name="service_code" value="{{.Filters.ServiceCode}}">
					</label>
					{{template "ui.submit-button" .Filters.ApplyButton}}
					{{if .Filters.HasFilters}}<a class="button-link secondary" href="{{.Filters.ClearPath}}">Clear</a>{{end}}
				</form>
			</section>

			<section class="clock-strip">
				<div>
					<h2>Simulator Clock</h2>
					<strong>{{.ClockCurrentTime}}</strong>
					<small>{{.ClockBillingPeriod}}</small>
				</div>
			</section>

			<section class="state-grid" aria-label="Bill state totals">
				{{range .StateCards}}
					<div class="state-card">
						<span>{{.Label}}</span>
						<strong>{{.Total}}</strong>
						<small>{{.Count}} bills</small>
					</div>
				{{end}}
			</section>

			<section>
				<div class="section-heading">
					<h2>Bill Reconciliation</h2>
					<span>{{.BillReconciliationsLabel}}</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.BillReconciliations}}
						<tbody>
							{{range .BillReconciliations}}
								<tr>
									<td><strong>{{.BillID}}</strong><small>{{.CurrencyCode}}</small></td>
									<td>{{.Period}}</td>
									<td>{{.PayerAccountID}}</td>
									<td><span class="status">{{.State}}</span></td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.BillLineItems}}</td>
									<td>{{.SourceLineItems}}</td>
									<td>{{.LineItemResidual}}</td>
									<td>{{.BillTotal}}</td>
									<td>{{.SourceTotal}}</td>
									<td><strong>{{.RoundingResidual}}</strong></td>
									<td>{{.ChargeResidual}}</td>
									<td>{{.CreditResidual}}</td>
									<td>{{.RefundResidual}}</td>
									<td>{{.TaxResidual}}</td>
									<td>{{.UpdatedAt}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.BillReconciliations}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Charges by Service and Account</h2>
					<span>{{.ChargeBreakdownsLabel}}</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.ChargeBreakdowns}}
						<tbody>
							{{range .ChargeBreakdowns}}
								<tr>
									<td>{{.Period}}</td>
									<td>{{.PayerAccountID}}</td>
									<td>{{.UsageAccountID}}</td>
									<td><strong>{{.ServiceName}}</strong><small>{{.ServiceCode}}</small></td>
									<td>{{.RegionCode}}</td>
									<td><code>{{.UsageType}}</code></td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.ResourceCount}}</td>
									<td>{{.LineItemCount}}</td>
									<td>{{.Charges}}</td>
									<td>{{.Credits}}</td>
									<td>{{.Refunds}}</td>
									<td>{{.Tax}}</td>
									<td><strong>{{.Total}}</strong></td>
									<td>{{.UpdatedAt}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.ChargeBreakdowns}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Resource Charge Drilldown</h2>
					<span>{{.ResourceBreakdownsLabel}}</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.ResourceChargeBreakdowns}}
						<tbody>
							{{range .ResourceChargeBreakdowns}}
								<tr>
									<td><strong>{{.Resource}}</strong><small>{{.ResourceID}}</small></td>
									<td>{{.Period}}</td>
									<td>{{.PayerAccountID}}</td>
									<td>{{.UsageAccountID}}</td>
									<td><strong>{{.ServiceName}}</strong><small>{{.ServiceCode}}</small></td>
									<td>{{.RegionCode}}</td>
									<td><code>{{.UsageType}}</code><small>{{.Description}}</small></td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.LineItemCount}}</td>
									<td>{{.Charges}}</td>
									<td>{{.Credits}}</td>
									<td>{{.Refunds}}</td>
									<td>{{.Tax}}</td>
									<td><strong>{{.Total}}</strong></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.ResourceChargeBreakdowns}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Bill States</h2>
					<span>{{.BillSummariesLabel}}</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.BillSummaries}}
						<tbody>
							{{range .BillSummaries}}
								<tr>
									<td><span class="status">{{.State}}</span><small>{{.ID}}</small></td>
									<td>{{.Period}}</td>
									<td>{{.PayerAccountID}}</td>
									<td>{{.LineItemCount}}</td>
									<td>{{.Charges}}</td>
									<td>{{.Credits}}</td>
									<td>{{.Refunds}}</td>
									<td>{{.Tax}}</td>
									<td><strong>{{.Total}}</strong></td>
									<td>
										{{if .InvoiceID}}
											{{if .CanViewInvoice}}
												<strong><a href="{{.InvoicePath}}">{{.InvoiceID}}</a></strong>
												<small>{{.InvoiceStatus}} due {{.InvoiceAmountDue}} paid {{.InvoicePaid}}</small>
												<small>{{.InvoiceDate}} to {{.InvoiceDueDate}}</small>
												<small><a href="{{.InvoiceCSVPath}}">Invoice CSV</a> <a href="{{.InvoicePDFPath}}">PDF</a></small>
											{{else}}
												<span class="muted">invoice restricted</span>
											{{end}}
											{{if .CURCSVPath}}<small><a href="{{.CURCSVPath}}">CUR CSV</a> <a href="{{.CURReconcilePath}}">Reconcile</a></small>{{end}}
										{{else if .InvoiceRestricted}}
											<span class="muted">invoice restricted</span>
											{{if .CURCSVPath}}<small><a href="{{.CURCSVPath}}">CUR CSV</a> <a href="{{.CURReconcilePath}}">Reconcile</a></small>{{end}}
										{{else}}
											<span class="muted">not issued</span>
										{{end}}
									</td>
									<td>{{.UpdatedAt}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.BillSummaries}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
{{end}}
`)

var invoicePageTemplate = newPageTemplate("invoice-page", `<div class="page-heading">
			<div>
				<h1>Invoice {{.InvoiceID}}</h1>
			</div>
			{{template "ui.action-bar" .Actions}}
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else if .Loaded}}
			<section class="invoice-document">
				<div class="invoice-title-row">
					<div>
						<span>Invoice</span>
						<strong>{{.InvoiceID}}</strong>
						<small>Bill {{.BillID}}</small>
					</div>
					<div>
						<span>Total</span>
						<strong>{{.Total}}</strong>
						<small>{{.PaymentStatus}} due {{.AmountDue}} paid {{.AmountPaid}}</small>
					</div>
				</div>

				<div class="invoice-meta-grid">
					<div class="detail-list">
						<span>Seller</span>
						<strong>{{.SellerOfRecord}}</strong>
						<small>{{.SellerAddress}}</small>
						<small>Tax registration {{.SellerTaxRegistration}}</small>
					</div>
					<div class="detail-list">
						<span>Bill To</span>
						<strong>{{.BillToName}}</strong>
						<small>{{.BillToEmail}}</small>
						<small>{{.BillToAddress}}</small>
						<small>Tax registration {{.BillToTaxRegistration}}</small>
					</div>
					<div class="detail-list">
						<span>Dates</span>
						<strong>{{.InvoiceDate}} to {{.DueDate}}</strong>
						<small>{{.BillingPeriod}}</small>
					</div>
					<div class="detail-list">
						<span>Status</span>
						<strong>{{.DocumentStatus}}</strong>
						<small>Document version {{.DocumentVersion}}</small>
						<small>{{.CurrencyCode}} payer {{.PayerAccountID}}</small>
					</div>
				</div>

				<div class="invoice-total-grid" aria-label="Invoice totals">
					<div><span>Charges</span><strong>{{.Charges}}</strong></div>
					<div><span>Credits</span><strong>{{.Credits}}</strong></div>
					<div><span>Refunds</span><strong>{{.Refunds}}</strong></div>
					<div><span>Tax</span><strong>{{.Tax}}</strong></div>
					<div><span>Line Items</span><strong>{{.LineItemCount}}</strong></div>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Service Detail</h2>
					<span>{{len .ServiceSummaries}} services</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.ServiceSummaries}}
						<tbody>
							{{range .ServiceSummaries}}
								<tr>
									<td><strong>{{.ServiceName}}</strong><small>{{.ServiceCode}} {{.CurrencyCode}}</small></td>
									<td>{{.LineItemCount}}</td>
									<td>{{.Charges}}</td>
									<td>{{.Credits}}</td>
									<td>{{.Refunds}}</td>
									<td>{{.Tax}}</td>
									<td><strong>{{.Total}}</strong></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.ServiceSummaries}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Account Detail</h2>
					<span>{{len .AccountSummaries}} accounts</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.AccountSummaries}}
						<tbody>
							{{range .AccountSummaries}}
								<tr>
									<td><strong>{{.UsageAccountID}}</strong><small>{{.CurrencyCode}}</small></td>
									<td>{{.LineItemCount}}</td>
									<td>{{.Charges}}</td>
									<td>{{.Credits}}</td>
									<td>{{.Refunds}}</td>
									<td>{{.Tax}}</td>
									<td><strong>{{.Total}}</strong></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.AccountSummaries}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Invoice Lines</h2>
					<span>{{len .LineItems}} rows</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.LineItems}}
						<tbody>
							{{range .LineItems}}
								<tr>
									<td><strong>{{.Resource}}</strong><small>{{.ResourceID}}</small></td>
									<td>{{.UsageAccountID}}</td>
									<td><strong>{{.ServiceName}}</strong><small>{{.ServiceCode}}</small></td>
									<td><span class="status">{{.LineItemType}}</span></td>
									<td>{{.RegionCode}}</td>
									<td><code>{{.UsageType}}</code><small>{{.Operation}}</small><small>{{.Description}}</small></td>
									<td>{{.Window}}</td>
									<td>{{.Quantity}}</td>
									<td>{{.Rate}}</td>
									<td><strong>{{.Cost}}</strong></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.LineItems}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
`)
