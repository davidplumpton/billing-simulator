package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

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
	ClockCurrentTime         string
	ClockBillingPeriod       string
	StateCards               []billStateCardView
	BillSummaries            []billSummaryView
	BillReconciliations      []billReconciliationView
	ChargeBreakdowns         []billChargeBreakdownView
	ResourceChargeBreakdowns []billResourceChargeBreakdownView
}

type billStateCardView struct {
	Key   string
	Label string
	Count int
	Total string
}

type billSummaryView struct {
	ID               string
	Period           string
	PayerAccountID   string
	State            string
	LineItemCount    int
	Charges          string
	Credits          string
	Refunds          string
	Tax              string
	Total            string
	InvoiceID        string
	InvoicePath      string
	InvoiceStatus    string
	InvoiceAmountDue string
	InvoicePaid      string
	InvoiceDate      string
	InvoiceDueDate   string
	UpdatedAt        string
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
	ServiceSummaries      []invoiceChargeSummaryView
	AccountSummaries      []invoiceAccountChargeSummaryView
	LineItems             []invoiceLineItemView
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
		methodNotAllowed(w)
		return
	}
	h.renderBills(w, r, http.StatusOK, "")
}

// handleInvoice serves one printable synthetic invoice document.
func (h billsHandler) handleInvoice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	invoiceID, ok := invoiceIDFromPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.renderInvoice(w, r, http.StatusOK, invoiceID, "")
}

// renderBills builds the dedicated bills state page from the current workspace.
func (h billsHandler) renderBills(w http.ResponseWriter, r *http.Request, status int, errorMessage string) {
	data := billsPageData{
		WorkspaceReady: h.db != nil,
		Error:          errorMessage,
		StateCards:     billStateCards(nil),
	}
	if h.db != nil {
		if err := h.loadBillsPageData(r.Context(), &data); err != nil {
			status = http.StatusInternalServerError
			data.Error = err.Error()
		}
	}

	var body bytes.Buffer
	if err := billsPageTemplate.Execute(&body, data); err != nil {
		http.Error(w, "render bills page: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = body.WriteTo(w)
}

// renderInvoice builds the printable invoice page from the invoice read model.
func (h billsHandler) renderInvoice(w http.ResponseWriter, r *http.Request, status int, invoiceID, errorMessage string) {
	data := invoicePageData{
		WorkspaceReady: h.db != nil,
		InvoiceID:      invoiceID,
		Error:          errorMessage,
	}
	if h.db != nil && errorMessage == "" {
		printable, err := h.invoices.GetPrintableByInvoiceID(r.Context(), invoiceID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
				data.Error = "Invoice not found."
			} else {
				status = http.StatusInternalServerError
				data.Error = err.Error()
			}
		} else {
			data = invoicePageDataFromPrintable(printable)
		}
	}

	var body bytes.Buffer
	if err := invoicePageTemplate.Execute(&body, data); err != nil {
		http.Error(w, "render invoice page: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = body.WriteTo(w)
}

// loadBillsPageData reads clock context and bill summaries for rendering.
func (h billsHandler) loadBillsPageData(ctx context.Context, data *billsPageData) error {
	clock, err := h.clock.Get(ctx)
	if err != nil {
		return err
	}
	data.ClockCurrentTime = clock.CurrentTime
	data.ClockBillingPeriod = fmt.Sprintf("%s to %s (%d days)", clock.BillingPeriodStart, clock.BillingPeriodEnd, clock.BillingPeriodDays)

	summaries, err := h.bills.ListBillStateSummaries(ctx, persistence.BillStateSummaryRequest{
		Limit:                 50,
		DefaultPayerAccountID: defaultAccountID,
	})
	if err != nil {
		return err
	}
	data.StateCards = billStateCards(summaries)
	for _, summary := range summaries {
		data.BillSummaries = append(data.BillSummaries, billSummaryViewFromSummary(summary))
	}

	reconciliations, err := h.bills.ListBillReconciliations(ctx, persistence.BillReconciliationRequest{
		Limit: 50,
	})
	if err != nil {
		return err
	}
	for _, reconciliation := range reconciliations {
		data.BillReconciliations = append(data.BillReconciliations, billReconciliationViewFromSummary(reconciliation))
	}

	breakdowns, err := h.bills.ListChargeBreakdowns(ctx, persistence.BillChargeBreakdownRequest{
		Limit: 75,
	})
	if err != nil {
		return err
	}
	for _, summary := range breakdowns.Summaries {
		data.ChargeBreakdowns = append(data.ChargeBreakdowns, billChargeBreakdownViewFromSummary(summary))
	}
	for _, summary := range breakdowns.Resources {
		data.ResourceChargeBreakdowns = append(data.ResourceChargeBreakdowns, billResourceChargeBreakdownViewFromSummary(summary))
	}
	return nil
}

func billSummaryViewFromSummary(summary persistence.BillStateSummary) billSummaryView {
	invoicePath := ""
	if strings.TrimSpace(summary.InvoiceID) != "" {
		invoicePath = invoicePathForID(summary.InvoiceID)
	}
	return billSummaryView{
		ID:               summary.ID,
		Period:           summary.BillingPeriodStart + " to " + summary.BillingPeriodEnd,
		PayerAccountID:   summary.PayerAccountID,
		State:            displayBillState(summary.BillState),
		LineItemCount:    summary.LineItemCount,
		Charges:          formatUSDMicros(summary.UsageChargeMicros),
		Credits:          formatUSDMicros(summary.CreditMicros),
		Refunds:          formatUSDMicros(summary.RefundMicros),
		Tax:              formatUSDMicros(summary.TaxMicros),
		Total:            formatUSDMicros(summary.TotalMicros),
		InvoiceID:        summary.InvoiceID,
		InvoicePath:      invoicePath,
		InvoiceStatus:    displayBillState(summary.InvoiceStatus),
		InvoiceAmountDue: formatUSDMicros(summary.InvoiceAmountDueMicros),
		InvoicePaid:      formatUSDMicros(summary.InvoiceAmountPaidMicros),
		InvoiceDate:      summary.InvoiceDate,
		InvoiceDueDate:   summary.InvoiceDueDate,
		UpdatedAt:        summary.UpdatedAt,
	}
}

// invoicePageDataFromPrintable prepares a printable invoice for the HTML template.
func invoicePageDataFromPrintable(printable persistence.PrintableInvoice) invoicePageData {
	document := printable.Document
	obligation := printable.Obligation
	data := invoicePageData{
		WorkspaceReady:        true,
		Loaded:                true,
		InvoiceID:             document.InvoiceID,
		BillID:                document.BillID,
		DocumentVersion:       document.DocumentVersion,
		DocumentStatus:        displayBillState(document.Status),
		BillingPeriod:         document.BillingPeriodStart + " to " + document.BillingPeriodEnd,
		InvoiceDate:           document.InvoiceDate,
		DueDate:               document.DueDate,
		SellerOfRecord:        document.SellerOfRecord,
		SellerAddress:         document.SellerAddress,
		SellerTaxRegistration: displayOptionalValue(document.SellerTaxRegistration),
		PayerAccountID:        document.PayerAccountID,
		BillToName:            document.BillToName,
		BillToEmail:           document.BillToEmail,
		BillToAddress:         document.BillToAddress,
		BillToTaxRegistration: displayOptionalValue(document.BillToTaxRegistration),
		CurrencyCode:          document.CurrencyCode,
		LineItemCount:         document.LineItemCount,
		Charges:               formatUSDMicros(document.UsageChargeMicros),
		Credits:               formatUSDMicros(document.CreditMicros),
		Refunds:               formatUSDMicros(document.RefundMicros),
		Tax:                   formatUSDMicros(document.TaxMicros),
		Total:                 formatUSDMicros(document.TotalMicros),
		PaymentStatus:         displayBillState(obligation.Status),
		AmountDue:             formatUSDMicros(obligation.AmountDueMicros),
		AmountPaid:            formatUSDMicros(obligation.AmountPaidMicros),
	}
	for _, summary := range printable.ServiceSummaries {
		data.ServiceSummaries = append(data.ServiceSummaries, invoiceChargeSummaryViewFromSummary(summary))
	}
	for _, summary := range printable.AccountSummaries {
		data.AccountSummaries = append(data.AccountSummaries, invoiceAccountChargeSummaryViewFromSummary(summary))
	}
	for _, item := range printable.LineItems {
		data.LineItems = append(data.LineItems, invoiceLineItemViewFromItem(item))
	}
	return data
}

// invoiceChargeSummaryViewFromSummary formats one service-level invoice rollup.
func invoiceChargeSummaryViewFromSummary(summary persistence.InvoiceChargeSummary) invoiceChargeSummaryView {
	return invoiceChargeSummaryView{
		ServiceCode:   summary.ServiceCode,
		ServiceName:   summary.ServiceName,
		CurrencyCode:  summary.CurrencyCode,
		LineItemCount: summary.LineItemCount,
		Charges:       formatUSDMicros(summary.ChargeMicros),
		Credits:       formatUSDMicros(summary.CreditMicros),
		Refunds:       formatUSDMicros(summary.RefundMicros),
		Tax:           formatUSDMicros(summary.TaxMicros),
		Total:         formatUSDMicros(summary.TotalMicros),
	}
}

// invoiceAccountChargeSummaryViewFromSummary formats one usage-account invoice rollup.
func invoiceAccountChargeSummaryViewFromSummary(summary persistence.InvoiceAccountChargeSummary) invoiceAccountChargeSummaryView {
	return invoiceAccountChargeSummaryView{
		UsageAccountID: summary.UsageAccountID,
		CurrencyCode:   summary.CurrencyCode,
		LineItemCount:  summary.LineItemCount,
		Charges:        formatUSDMicros(summary.ChargeMicros),
		Credits:        formatUSDMicros(summary.CreditMicros),
		Refunds:        formatUSDMicros(summary.RefundMicros),
		Tax:            formatUSDMicros(summary.TaxMicros),
		Total:          formatUSDMicros(summary.TotalMicros),
	}
}

// invoiceLineItemViewFromItem formats one final source line item for invoice display.
func invoiceLineItemViewFromItem(item persistence.InvoiceLineItem) invoiceLineItemView {
	resource := strings.TrimSpace(item.ResourceName)
	if resource == "" {
		resource = strings.TrimSpace(item.ResourceID)
	}
	if resource == "" {
		resource = "Period level"
	}
	return invoiceLineItemView{
		ID:             item.ID,
		Resource:       resource,
		ResourceID:     item.ResourceID,
		UsageAccountID: item.UsageAccountID,
		ServiceCode:    item.ServiceCode,
		ServiceName:    item.ServiceName,
		LineItemType:   item.LineItemType,
		RegionCode:     item.RegionCode,
		UsageType:      item.UsageType,
		Operation:      item.Operation,
		Window:         item.UsageStartTime + " to " + item.UsageEndTime,
		Quantity:       formatQuantityMicros(item.PricingQuantityMicros) + " " + item.PricingUnit,
		Rate:           formatUSDMicros(item.UnblendedRateMicros) + "/" + item.PricingUnit,
		Cost:           formatUSDMicros(item.UnblendedCostMicros),
		Description:    item.Description,
	}
}

func billChargeBreakdownViewFromSummary(summary persistence.BillChargeSummary) billChargeBreakdownView {
	return billChargeBreakdownView{
		Period:         summary.BillingPeriodStart + " to " + summary.BillingPeriodEnd,
		PayerAccountID: summary.PayerAccountID,
		UsageAccountID: summary.UsageAccountID,
		ServiceCode:    summary.ServiceCode,
		ServiceName:    summary.ServiceName,
		RegionCode:     summary.RegionCode,
		UsageType:      summary.UsageType,
		Status:         displayBillState(summary.LineItemStatus),
		ResourceCount:  summary.ResourceCount,
		LineItemCount:  summary.LineItemCount,
		Charges:        formatUSDMicros(summary.ChargeMicros),
		Credits:        formatUSDMicros(summary.CreditMicros),
		Refunds:        formatUSDMicros(summary.RefundMicros),
		Tax:            formatUSDMicros(summary.TaxMicros),
		Total:          formatUSDMicros(summary.TotalMicros),
		UpdatedAt:      summary.UpdatedAt,
	}
}

func billResourceChargeBreakdownViewFromSummary(summary persistence.BillResourceChargeSummary) billResourceChargeBreakdownView {
	resource := strings.TrimSpace(summary.ResourceName)
	if resource == "" {
		resource = strings.TrimSpace(summary.ResourceID)
	}
	if resource == "" {
		resource = "Period level"
	}
	return billResourceChargeBreakdownView{
		Resource:       resource,
		ResourceID:     summary.ResourceID,
		Period:         summary.BillingPeriodStart + " to " + summary.BillingPeriodEnd,
		PayerAccountID: summary.PayerAccountID,
		UsageAccountID: summary.UsageAccountID,
		ServiceCode:    summary.ServiceCode,
		ServiceName:    summary.ServiceName,
		RegionCode:     summary.RegionCode,
		UsageType:      summary.UsageType,
		Status:         displayBillState(summary.LineItemStatus),
		LineItemCount:  summary.LineItemCount,
		Charges:        formatUSDMicros(summary.ChargeMicros),
		Credits:        formatUSDMicros(summary.CreditMicros),
		Refunds:        formatUSDMicros(summary.RefundMicros),
		Tax:            formatUSDMicros(summary.TaxMicros),
		Total:          formatUSDMicros(summary.TotalMicros),
		Description:    summary.Description,
	}
}

func billReconciliationViewFromSummary(reconciliation persistence.BillReconciliation) billReconciliationView {
	return billReconciliationView{
		BillID:           reconciliation.BillID,
		Period:           reconciliation.BillingPeriodStart + " to " + reconciliation.BillingPeriodEnd,
		PayerAccountID:   reconciliation.PayerAccountID,
		State:            displayBillState(reconciliation.BillState),
		Status:           displayBillState(reconciliation.Status),
		CurrencyCode:     reconciliation.CurrencyCode,
		BillLineItems:    reconciliation.BillLineItemCount,
		SourceLineItems:  reconciliation.SourceLineItemCount,
		LineItemResidual: reconciliation.LineItemCountResidual,
		BillTotal:        formatUSDMicros(reconciliation.BillTotalMicros),
		SourceTotal:      formatUSDMicros(reconciliation.SourceTotalMicros),
		RoundingResidual: formatUSDMicros(reconciliation.TotalResidualMicros),
		ChargeResidual:   formatUSDMicros(reconciliation.UsageChargeResidualMicros),
		CreditResidual:   formatUSDMicros(reconciliation.CreditResidualMicros),
		RefundResidual:   formatUSDMicros(reconciliation.RefundResidualMicros),
		TaxResidual:      formatUSDMicros(reconciliation.TaxResidualMicros),
		UpdatedAt:        reconciliation.UpdatedAt,
	}
}

func billStateCards(summaries []persistence.BillStateSummary) []billStateCardView {
	counts := map[string]int{}
	totals := map[string]int64{}
	for _, summary := range summaries {
		counts[summary.BillState]++
		totals[summary.BillState] += summary.TotalMicros
	}

	definitions := billStateDefinitions()
	cards := make([]billStateCardView, 0, len(definitions))
	for _, definition := range definitions {
		cards = append(cards, billStateCardView{
			Key:   definition.Key,
			Label: definition.Label,
			Count: counts[definition.Key],
			Total: formatUSDMicros(totals[definition.Key]),
		})
	}
	return cards
}

func billStateDefinitions() []billStateDefinition {
	return []billStateDefinition{
		{Key: "open", Label: "Open"},
		{Key: "pending-close", Label: "Pending Close"},
		{Key: "issued", Label: "Issued"},
		{Key: "adjusted", Label: "Adjusted"},
		{Key: "paid", Label: "Paid"},
		{Key: "past_due", Label: "Past Due"},
	}
}

func displayBillState(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ReplaceAll(value, "_", "-")
}

// displayOptionalValue gives blank optional invoice profile fields a stable printable label.
func displayOptionalValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

// invoiceIDFromPath extracts and unescapes the invoice ID from /invoices/{id}.
func invoiceIDFromPath(path string) (string, bool) {
	const prefix = "/invoices/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rawID := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	if rawID == "" || strings.Contains(rawID, "/") {
		return "", false
	}
	invoiceID, err := url.PathUnescape(rawID)
	if err != nil || strings.TrimSpace(invoiceID) == "" {
		return "", false
	}
	return strings.TrimSpace(invoiceID), true
}

// invoicePathForID escapes an invoice ID for use as an internal invoice link.
func invoicePathForID(invoiceID string) string {
	return "/invoices/" + url.PathEscape(strings.TrimSpace(invoiceID))
}

var billsPageTemplate = template.Must(template.New("bills-page").Parse(`<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Bills - AWS Billing Simulator</title>
	<link rel="stylesheet" href="/assets/app.css">
</head>
<body>
	<header class="topbar">
		<div class="brand">AWS Billing Simulator</div>
		<nav aria-label="Primary">
			<a href="/workspaces">Workspaces</a>
			<a href="/resources">Resources</a>
			<span>Tags</span>
			<span>Cost Explorer</span>
			<a class="active" href="/bills">Bills</a>
			<span>Scenarios</span>
		</nav>
	</header>

	<main class="page">
		<div class="page-heading">
			<div>
				<h1>Bills</h1>
			</div>
		</div>

		{{if .Error}}<div class="notice error">{{.Error}}</div>{{end}}

		{{if not .WorkspaceReady}}
			<section class="empty">
				<h2>Workspace Required</h2>
				<p>No workspace is open.</p>
				<a class="button-link" href="/workspaces">Open Workspace</a>
			</section>
		{{else}}
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
					<span>{{len .BillReconciliations}} bills</span>
				</div>
				<div class="table-wrap">
					<table>
						<thead>
							<tr>
								<th>Bill</th>
								<th>Period</th>
								<th>Payer</th>
								<th>State</th>
								<th>Status</th>
								<th>Bill Items</th>
								<th>Source Items</th>
								<th>Item Delta</th>
								<th>Bill Total</th>
								<th>Source Total</th>
								<th>Rounding Residual</th>
								<th>Charge Residual</th>
								<th>Credit Residual</th>
								<th>Refund Residual</th>
								<th>Tax Residual</th>
								<th>Updated</th>
							</tr>
						</thead>
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
								<tr><td colspan="16" class="empty-cell">No issued bills to reconcile</td></tr>
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Charges by Service and Account</h2>
					<span>{{len .ChargeBreakdowns}} groups</span>
				</div>
				<div class="table-wrap">
					<table>
						<thead>
							<tr>
								<th>Period</th>
								<th>Payer</th>
								<th>Usage Account</th>
								<th>Service</th>
								<th>Region</th>
								<th>Usage Type</th>
								<th>Status</th>
								<th>Resources</th>
								<th>Items</th>
								<th>Charges</th>
								<th>Credits</th>
								<th>Refunds</th>
								<th>Tax</th>
								<th>Total</th>
								<th>Updated</th>
							</tr>
						</thead>
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
								<tr><td colspan="15" class="empty-cell">No charges</td></tr>
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Resource Charge Drilldown</h2>
					<span>{{len .ResourceChargeBreakdowns}} rows</span>
				</div>
				<div class="table-wrap">
					<table>
						<thead>
							<tr>
								<th>Resource</th>
								<th>Period</th>
								<th>Payer</th>
								<th>Usage Account</th>
								<th>Service</th>
								<th>Region</th>
								<th>Usage Type</th>
								<th>Status</th>
								<th>Items</th>
								<th>Charges</th>
								<th>Credits</th>
								<th>Refunds</th>
								<th>Tax</th>
								<th>Total</th>
							</tr>
						</thead>
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
								<tr><td colspan="14" class="empty-cell">No resource charges</td></tr>
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Bill States</h2>
					<span>{{len .BillSummaries}} summaries</span>
				</div>
				<div class="table-wrap">
					<table>
						<thead>
							<tr>
								<th>State</th>
								<th>Period</th>
								<th>Payer</th>
								<th>Items</th>
								<th>Charges</th>
								<th>Credits</th>
								<th>Refunds</th>
								<th>Tax</th>
								<th>Total</th>
								<th>Invoice</th>
								<th>Updated</th>
							</tr>
						</thead>
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
											<strong><a href="{{.InvoicePath}}">{{.InvoiceID}}</a></strong>
											<small>{{.InvoiceStatus}} due {{.InvoiceAmountDue}} paid {{.InvoicePaid}}</small>
											<small>{{.InvoiceDate}} to {{.InvoiceDueDate}}</small>
										{{else}}
											<span class="muted">not issued</span>
										{{end}}
									</td>
									<td>{{.UpdatedAt}}</td>
								</tr>
							{{else}}
								<tr><td colspan="11" class="empty-cell">No bill states</td></tr>
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
	</main>
</body>
</html>
`))

var invoicePageTemplate = template.Must(template.New("invoice-page").Parse(`<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Invoice {{.InvoiceID}} - AWS Billing Simulator</title>
	<link rel="stylesheet" href="/assets/app.css">
</head>
<body>
	<header class="topbar">
		<div class="brand">AWS Billing Simulator</div>
		<nav aria-label="Primary">
			<a href="/workspaces">Workspaces</a>
			<a href="/resources">Resources</a>
			<span>Tags</span>
			<span>Cost Explorer</span>
			<a class="active" href="/bills">Bills</a>
			<span>Scenarios</span>
		</nav>
	</header>

	<main class="page invoice-page">
		<div class="page-heading">
			<div>
				<h1>Invoice {{.InvoiceID}}</h1>
			</div>
			<a class="button-link" href="/bills">Bills</a>
		</div>

		{{if .Error}}<div class="notice error">{{.Error}}</div>{{end}}

		{{if not .WorkspaceReady}}
			<section class="empty">
				<h2>Workspace Required</h2>
				<p>No workspace is open.</p>
				<a class="button-link" href="/workspaces">Open Workspace</a>
			</section>
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
					<table>
						<thead>
							<tr>
								<th>Service</th>
								<th>Items</th>
								<th>Charges</th>
								<th>Credits</th>
								<th>Refunds</th>
								<th>Tax</th>
								<th>Total</th>
							</tr>
						</thead>
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
								<tr><td colspan="7" class="empty-cell">No service detail</td></tr>
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
					<table>
						<thead>
							<tr>
								<th>Usage Account</th>
								<th>Items</th>
								<th>Charges</th>
								<th>Credits</th>
								<th>Refunds</th>
								<th>Tax</th>
								<th>Total</th>
							</tr>
						</thead>
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
								<tr><td colspan="7" class="empty-cell">No account detail</td></tr>
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
					<table>
						<thead>
							<tr>
								<th>Resource</th>
								<th>Account</th>
								<th>Service</th>
								<th>Type</th>
								<th>Region</th>
								<th>Usage</th>
								<th>Window</th>
								<th>Quantity</th>
								<th>Rate</th>
								<th>Cost</th>
							</tr>
						</thead>
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
								<tr><td colspan="10" class="empty-cell">No invoice lines</td></tr>
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
	</main>
</body>
</html>
`))
