package app

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

type billsHandler struct {
	db    *sql.DB
	bills persistence.BillsRepository
	clock persistence.SimulatorClockRepository
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

type billStateDefinition struct {
	Key   string
	Label string
}

// newBillsHandler builds the repositories needed for the Bills page.
func newBillsHandler(db *sql.DB) billsHandler {
	return billsHandler{
		db:    db,
		bills: persistence.NewBillsRepository(db),
		clock: persistence.NewSimulatorClockRepository(db),
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
		InvoiceStatus:    displayBillState(summary.InvoiceStatus),
		InvoiceAmountDue: formatUSDMicros(summary.InvoiceAmountDueMicros),
		InvoicePaid:      formatUSDMicros(summary.InvoiceAmountPaidMicros),
		InvoiceDate:      summary.InvoiceDate,
		InvoiceDueDate:   summary.InvoiceDueDate,
		UpdatedAt:        summary.UpdatedAt,
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
											<strong>{{.InvoiceID}}</strong>
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
