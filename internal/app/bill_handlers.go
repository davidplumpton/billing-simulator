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
	WorkspaceReady     bool
	Error              string
	ClockCurrentTime   string
	ClockBillingPeriod string
	StateCards         []billStateCardView
	BillSummaries      []billSummaryView
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
