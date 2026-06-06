package app

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

type paymentsHandler struct {
	db        *sql.DB
	closes    persistence.MonthEndCloseRepository
	lifecycle persistence.PaymentLifecycleRepository
	profiles  persistence.PaymentProfileRepository
	clock     persistence.SimulatorClockRepository
}

type paymentsPageData struct {
	WorkspaceReady      bool
	Flash               string
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	ClockCurrentTime    string
	ClockBillingPeriod  string
	StateCards          []billStateCardView
	Invoices            []paymentInvoiceView
	History             []paymentEventView
	PaymentMethods      []paymentMethodRowView
	Tables              paymentsTablesView
}

type paymentInvoiceView struct {
	InvoiceObligationID string
	InvoiceID           string
	InvoicePath         string
	BillID              string
	BillingPeriod       string
	PayerAccountID      string
	BillState           string
	PaymentStatus       string
	AmountDue           string
	AmountPaid          string
	AmountDueValue      string
	AmountPaidValue     string
	InvoiceDate         string
	DueDate             string
	PastDueDate         string
	LastFailureReason   string
	CanSchedule         bool
	CanProcess          bool
	CanFail             bool
	CanMarkDue          bool
	CanMarkPastDue      bool
	CanCollect          bool
	CanRefund           bool
}

type paymentEventView struct {
	InvoiceID    string
	Transition   string
	FromStatus   string
	ToStatus     string
	Amount       string
	CurrencyCode string
	Reason       string
	OccurredAt   string
	CreatedAt    string
}

type paymentMethodRowView struct {
	PayerAccountID    string
	ProfileID         string
	ProfileName       string
	BillTo            string
	MethodID          string
	MethodDisplayName string
	MethodType        string
	Status            string
	DefaultLabel      string
	UnappliedFunds    string
	FailureReason     string
	CanSetDefault     bool
	CanFix            bool
}

type paymentsTablesView struct {
	Invoices       uiTableView
	PaymentHistory uiTableView
	PaymentMethods uiTableView
}

// newPaymentsHandler builds the repositories needed for simulated payment operations.
func newPaymentsHandler(db *sql.DB) paymentsHandler {
	return paymentsHandler{
		db:        db,
		closes:    persistence.NewMonthEndCloseRepository(db),
		lifecycle: persistence.NewPaymentLifecycleRepository(db),
		profiles:  persistence.NewPaymentProfileRepository(db),
		clock:     persistence.NewSimulatorClockRepository(db),
	}
}

// handlePayments serves the payment operations workspace.
func (h paymentsHandler) handlePayments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	h.renderPayments(w, r, http.StatusOK, "", flashFromQuery(r))
}

// handlePaymentAction applies one simulated invoice or profile payment action.
func (h paymentsHandler) handlePaymentAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		h.renderPayments(w, r, http.StatusServiceUnavailable, "Open a workspace before managing payments.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderPayments(w, r, http.StatusBadRequest, "parse payment form: "+err.Error(), "")
		return
	}
	flash, err := h.applyPaymentAction(r.Context(), r)
	if err != nil {
		h.renderPayments(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/payments?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

func (h paymentsHandler) renderPayments(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	data := paymentsPageData{
		WorkspaceReady:      h.db != nil,
		Flash:               flashMessage,
		Error:               errorMessage,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Tables:              paymentsTables(),
	}
	if h.db != nil {
		if err := h.loadPaymentsPageData(r.Context(), &data); err != nil {
			status = http.StatusInternalServerError
			data.Error = err.Error()
		}
	}
	data.Notices = uiNotices(data.Flash, data.Error)

	renderPage(w, status, pageLayoutOptions{
		Title:     "Payments - AWS Billing Simulator",
		ActiveNav: "payments",
	}, paymentsPageTemplate, data, "render payments page")
}

// loadPaymentsPageData assembles invoices, lifecycle history, and profile setup for payment workflows.
func (h paymentsHandler) loadPaymentsPageData(ctx context.Context, data *paymentsPageData) error {
	clock, err := h.clock.Get(ctx)
	if err != nil {
		return err
	}
	data.ClockCurrentTime = clock.CurrentTime
	data.ClockBillingPeriod = fmt.Sprintf("%s to %s (%d days)", clock.BillingPeriodStart, clock.BillingPeriodEnd, clock.BillingPeriodDays)

	issuedBills, err := h.closes.ListIssuedBills(ctx, 75)
	if err != nil {
		return err
	}
	data.StateCards = paymentStateCards(issuedBills)
	payerIDs := map[string]bool{}
	defaultPayerAccountID, err := defaultBillingPayerAccountID(ctx, h.db, "")
	if err != nil {
		return err
	}
	if defaultPayerAccountID != "" {
		payerIDs[defaultPayerAccountID] = true
	}

	for _, issued := range issuedBills {
		payerIDs[issued.Bill.PayerAccountID] = true

		events, err := h.lifecycle.ListEvents(ctx, issued.Obligation.ID, 10)
		if err != nil {
			return err
		}
		failureReason := recentFailureReason(events)
		if issued.Obligation.AmountDueMicros > 0 || issued.Obligation.Status != "succeeded" {
			data.Invoices = append(data.Invoices, paymentInvoiceViewFromIssuedBill(issued, failureReason))
		}
		for _, event := range events {
			data.History = append(data.History, paymentEventViewFromEvent(event, issued.Obligation.InvoiceID))
		}
	}
	sort.SliceStable(data.History, func(i, j int) bool {
		if data.History[i].OccurredAt == data.History[j].OccurredAt {
			return data.History[i].CreatedAt > data.History[j].CreatedAt
		}
		return data.History[i].OccurredAt > data.History[j].OccurredAt
	})
	payers := sortedPaymentPayers(payerIDs)
	for _, payerID := range payers {
		profiles, err := h.profiles.ListPaymentProfiles(ctx, payerID)
		if err != nil {
			return err
		}
		for _, profile := range profiles {
			methods, err := h.profiles.ListPaymentMethods(ctx, profile.ID)
			if err != nil {
				return err
			}
			if len(methods) == 0 {
				data.PaymentMethods = append(data.PaymentMethods, paymentMethodRowViewFromProfile(profile))
				continue
			}
			for _, method := range methods {
				data.PaymentMethods = append(data.PaymentMethods, paymentMethodRowViewFromMethod(profile, method))
			}
		}
	}
	return nil
}

func (h paymentsHandler) applyPaymentAction(ctx context.Context, r *http.Request) (string, error) {
	action := strings.TrimSpace(r.PostForm.Get("action"))
	switch action {
	case "schedule":
		request, err := h.transitionRequestFromForm(ctx, r, false)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.SchedulePayment(ctx, request)
		if err != nil {
			return "", err
		}
		return "Scheduled payment for " + result.Obligation.InvoiceID, nil
	case "process":
		request, err := h.transitionRequestFromForm(ctx, r, false)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.StartProcessing(ctx, request)
		if err != nil {
			return "", err
		}
		return "Started payment processing for " + result.Obligation.InvoiceID, nil
	case "fail":
		request, err := h.transitionRequestFromForm(ctx, r, false)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.FailPayment(ctx, request)
		if err != nil {
			return "", err
		}
		return "Recorded failed payment for " + result.Obligation.InvoiceID, nil
	case "mark_due":
		request, err := h.transitionRequestFromForm(ctx, r, false)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.MarkDue(ctx, request)
		if err != nil {
			return "", err
		}
		return "Marked " + result.Obligation.InvoiceID + " due", nil
	case "mark_past_due":
		request, err := h.transitionRequestFromForm(ctx, r, false)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.MarkPastDue(ctx, request)
		if err != nil {
			return "", err
		}
		return "Marked " + result.Obligation.InvoiceID + " past due", nil
	case "collect":
		request, err := h.transitionRequestFromForm(ctx, r, true)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.ApplyPayment(ctx, request)
		if err != nil {
			return "", err
		}
		return "Collected payment for " + result.Obligation.InvoiceID, nil
	case "refund":
		request, err := h.transitionRequestFromForm(ctx, r, true)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.RefundPayment(ctx, request)
		if err != nil {
			return "", err
		}
		return "Recorded refund for " + result.Obligation.InvoiceID, nil
	case "fix_method":
		method, err := h.profiles.ResolvePaymentMethodFailure(ctx, r.PostForm.Get("method_id"))
		if err != nil {
			return "", err
		}
		return "Fixed payment method " + method.DisplayName, nil
	case "set_default_method":
		method, err := h.profiles.SetDefaultPaymentMethod(ctx, r.PostForm.Get("method_id"))
		if err != nil {
			return "", err
		}
		return "Selected default payment method " + method.DisplayName, nil
	case "set_default_profile":
		profile, err := h.profiles.SetDefaultPaymentProfile(ctx, r.PostForm.Get("profile_id"))
		if err != nil {
			return "", err
		}
		return "Selected default payment profile " + profile.ProfileName, nil
	default:
		return "", fmt.Errorf("unsupported payment action %q", action)
	}
}

func (h paymentsHandler) transitionRequestFromForm(ctx context.Context, r *http.Request, includeAmount bool) (persistence.PaymentLifecycleTransitionRequest, error) {
	request := persistence.PaymentLifecycleTransitionRequest{
		InvoiceObligationID: r.PostForm.Get("invoice_obligation_id"),
		Reason:              r.PostForm.Get("reason"),
		OccurredAt:          strings.TrimSpace(r.PostForm.Get("occurred_at")),
	}
	if request.OccurredAt == "" {
		if clock, err := h.clock.Get(ctx); err == nil {
			request.OccurredAt = clock.CurrentTime
		}
	}
	if includeAmount {
		amount, err := parsePaymentAmountMicros(r.PostForm.Get("amount"))
		if err != nil {
			return persistence.PaymentLifecycleTransitionRequest{}, err
		}
		request.AmountMicros = amount
	}
	return request, nil
}

func paymentsTables() paymentsTablesView {
	return paymentsTablesView{
		Invoices:       uiTable(uiTableHeaders("Invoice", "Period", "Payer", "Bill", "Payment State", "Due", "Paid", "Due Date", "Failure", "Actions"), "No due invoices"),
		PaymentHistory: uiTable(uiTableHeaders("Invoice", "Transition", "From", "To", "Amount", "Reason", "Occurred", "Recorded"), "No payment history"),
		PaymentMethods: uiTable(uiTableHeaders("Payer", "Profile", "Bill To", "Method", "Status", "Default", "Unapplied Funds", "Failure", "Actions"), "No payment profiles"),
	}
}

func paymentInvoiceViewFromIssuedBill(issued persistence.BillWithInvoiceObligation, failureReason string) paymentInvoiceView {
	obligation := issued.Obligation
	status := strings.TrimSpace(obligation.Status)
	return paymentInvoiceView{
		InvoiceObligationID: obligation.ID,
		InvoiceID:           obligation.InvoiceID,
		InvoicePath:         invoicePathForID(obligation.InvoiceID),
		BillID:              issued.Bill.ID,
		BillingPeriod:       issued.Bill.BillingPeriodStart + " to " + issued.Bill.BillingPeriodEnd,
		PayerAccountID:      issued.Bill.PayerAccountID,
		BillState:           displayBillState(issued.Bill.BillState),
		PaymentStatus:       displayBillState(status),
		AmountDue:           formatUSDMicros(obligation.AmountDueMicros),
		AmountPaid:          formatUSDMicros(obligation.AmountPaidMicros),
		AmountDueValue:      formatMicrosDecimal(obligation.AmountDueMicros),
		AmountPaidValue:     formatMicrosDecimal(obligation.AmountPaidMicros),
		InvoiceDate:         obligation.InvoiceDate,
		DueDate:             obligation.DueDate,
		PastDueDate:         paymentPastDueDate(obligation.DueDate),
		LastFailureReason:   displayOptionalValue(failureReason),
		CanSchedule:         canSchedulePayment(status, obligation.AmountDueMicros),
		CanProcess:          canProcessPayment(status, obligation.AmountDueMicros),
		CanFail:             canFailPayment(status, obligation.AmountDueMicros),
		CanMarkDue:          canMarkPaymentDue(status, obligation.AmountDueMicros),
		CanMarkPastDue:      canMarkPaymentPastDue(status, obligation.AmountDueMicros),
		CanCollect:          canCollectPayment(status, obligation.AmountDueMicros),
		CanRefund:           canRefundPayment(status, obligation.AmountPaidMicros),
	}
}

func paymentEventViewFromEvent(event persistence.PaymentLifecycleEvent, invoiceID string) paymentEventView {
	return paymentEventView{
		InvoiceID:    invoiceID,
		Transition:   displayBillState(event.TransitionKind),
		FromStatus:   displayBillState(event.FromStatus),
		ToStatus:     displayBillState(event.ToStatus),
		Amount:       formatUSDMicros(event.AmountMicros),
		CurrencyCode: event.CurrencyCode,
		Reason:       displayOptionalValue(event.Reason),
		OccurredAt:   event.OccurredAt,
		CreatedAt:    event.CreatedAt,
	}
}

func paymentMethodRowViewFromProfile(profile persistence.PaymentProfile) paymentMethodRowView {
	return paymentMethodRowView{
		PayerAccountID:    profile.PayerAccountID,
		ProfileID:         profile.ID,
		ProfileName:       profile.ProfileName,
		BillTo:            paymentBillTo(profile),
		MethodDisplayName: "none",
		MethodType:        "none",
		Status:            displayBillState(profile.Status),
		DefaultLabel:      defaultLabel(profile.IsDefault),
		UnappliedFunds:    formatUSDMicros(0),
		FailureReason:     "none",
	}
}

func paymentMethodRowViewFromMethod(profile persistence.PaymentProfile, method persistence.PaymentMethod) paymentMethodRowView {
	status := strings.TrimSpace(method.Status)
	return paymentMethodRowView{
		PayerAccountID:    profile.PayerAccountID,
		ProfileID:         profile.ID,
		ProfileName:       profile.ProfileName,
		BillTo:            paymentBillTo(profile),
		MethodID:          method.ID,
		MethodDisplayName: method.DisplayName,
		MethodType:        displayBillState(method.MethodType),
		Status:            displayBillState(status),
		DefaultLabel:      defaultLabel(method.IsDefault),
		UnappliedFunds:    formatUSDMicros(method.AdvancePayBalanceMicros),
		FailureReason:     displayOptionalValue(method.FailureReason),
		CanSetDefault:     !method.IsDefault && status == "active" && profile.Status == "active",
		CanFix:            status == "failed" || status == "expired" || strings.TrimSpace(method.FailureReason) != "",
	}
}

func paymentBillTo(profile persistence.PaymentProfile) string {
	email := strings.TrimSpace(profile.BillToEmail)
	if email == "" {
		return profile.BillToName
	}
	return profile.BillToName + " <" + email + ">"
}

func defaultLabel(isDefault bool) string {
	if isDefault {
		return "default"
	}
	return ""
}

func recentFailureReason(events []persistence.PaymentLifecycleEvent) string {
	for _, event := range events {
		if event.ToStatus == "failed" && strings.TrimSpace(event.Reason) != "" {
			return event.Reason
		}
	}
	return ""
}

func sortedPaymentPayers(payers map[string]bool) []string {
	values := make([]string, 0, len(payers))
	for payerID := range payers {
		values = append(values, payerID)
	}
	sort.Strings(values)
	return values
}

func paymentStateCards(issuedBills []persistence.BillWithInvoiceObligation) []billStateCardView {
	counts := map[string]int{}
	totals := map[string]int64{}
	for _, issued := range issuedBills {
		status := strings.TrimSpace(issued.Obligation.Status)
		counts[status]++
		if status == "succeeded" {
			totals[status] += issued.Obligation.AmountPaidMicros
		} else {
			totals[status] += issued.Obligation.AmountDueMicros
		}
	}
	definitions := []billStateDefinition{
		{Key: "due", Label: "Due"},
		{Key: "scheduled", Label: "Scheduled"},
		{Key: "processing", Label: "Processing"},
		{Key: "failed", Label: "Failed"},
		{Key: "past_due", Label: "Past Due"},
		{Key: "partially_paid", Label: "Partially Paid"},
		{Key: "succeeded", Label: "Succeeded"},
	}
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

func canSchedulePayment(status string, amountDueMicros int64) bool {
	return amountDueMicros > 0 && matchesAny(status, "due", "failed", "past_due", "partially_paid", "refunded")
}

func canProcessPayment(status string, amountDueMicros int64) bool {
	return amountDueMicros > 0 && matchesAny(status, "due", "scheduled", "failed", "past_due", "partially_paid", "refunded")
}

func canFailPayment(status string, amountDueMicros int64) bool {
	return amountDueMicros > 0 && matchesAny(status, "scheduled", "processing", "partially_paid")
}

func canMarkPaymentDue(status string, amountDueMicros int64) bool {
	return amountDueMicros > 0 && matchesAny(status, "scheduled", "failed", "past_due", "refunded")
}

func canMarkPaymentPastDue(status string, amountDueMicros int64) bool {
	return amountDueMicros > 0 && matchesAny(status, "due", "scheduled", "processing", "failed", "partially_paid", "refunded")
}

func canCollectPayment(status string, amountDueMicros int64) bool {
	return amountDueMicros > 0 && matchesAny(status, "due", "processing", "failed", "past_due", "partially_paid", "refunded")
}

func canRefundPayment(status string, amountPaidMicros int64) bool {
	return amountPaidMicros > 0 && matchesAny(status, "succeeded", "partially_paid")
}

func matchesAny(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func parsePaymentAmountMicros(value string) (int64, error) {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "$", ""), ",", ""))
	if value == "" {
		return 0, fmt.Errorf("payment amount is required")
	}
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("payment amount must be numeric: %w", err)
	}
	if amount <= 0 {
		return 0, fmt.Errorf("payment amount must be greater than zero")
	}
	micros := math.Round(amount * 1_000_000)
	if micros > float64(math.MaxInt64) {
		return 0, fmt.Errorf("payment amount is too large")
	}
	return int64(micros), nil
}

func paymentPastDueDate(dueDate string) string {
	parsed, err := time.Parse(time.DateOnly, strings.TrimSpace(dueDate))
	if err != nil {
		return dueDate
	}
	return parsed.AddDate(0, 0, 1).Format(time.DateOnly)
}

var paymentsPageTemplate = newPageTemplate("payments-page", `<div class="page-heading">
			<div>
				<h1>Payments</h1>
			</div>
			<div class="page-actions">
				<a class="button-link secondary" href="/bills">Bills</a>
			</div>
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="clock-strip">
				<div>
					<h2>Simulator Clock</h2>
					<strong>{{.ClockCurrentTime}}</strong>
					<small>{{.ClockBillingPeriod}}</small>
				</div>
			</section>

			<section class="state-grid" aria-label="Payment state totals">
				{{range .StateCards}}
					<div class="state-card">
						<span>{{.Label}}</span>
						<strong>{{.Total}}</strong>
						<small>{{.Count}} invoices</small>
					</div>
				{{end}}
			</section>

			<section>
				<div class="section-heading">
					<h2>Due Invoices</h2>
					<span>{{len .Invoices}} invoices</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table payments-table">
						{{template "ui.dense-table-head" .Tables.Invoices}}
						<tbody>
							{{range .Invoices}}
								<tr>
									<td><a href="{{.InvoicePath}}"><strong>{{.InvoiceID}}</strong></a><small>{{.InvoiceDate}}</small></td>
									<td>{{.BillingPeriod}}</td>
									<td>{{.PayerAccountID}}</td>
									<td><strong>{{.BillID}}</strong><small>{{.BillState}}</small></td>
									<td><span class="status">{{.PaymentStatus}}</span></td>
									<td>{{.AmountDue}}</td>
									<td>{{.AmountPaid}}</td>
									<td>{{.DueDate}}</td>
									<td>{{.LastFailureReason}}</td>
									<td class="actions-cell">
										<div class="inline-actions">
											{{if .CanSchedule}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<button name="action" value="schedule" type="submit">Schedule</button>
												</form>
											{{end}}
											{{if .CanProcess}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<button name="action" value="process" type="submit">Retry</button>
												</form>
											{{end}}
											{{if .CanMarkDue}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<button name="action" value="mark_due" type="submit">Mark Due</button>
												</form>
											{{end}}
											{{if .CanMarkPastDue}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<label>As Of
														<input type="date" name="occurred_at" value="{{.PastDueDate}}">
													</label>
													<button name="action" value="mark_past_due" type="submit">Past Due</button>
												</form>
											{{end}}
											{{if .CanFail}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<label>Reason
														<input name="reason" value="Payment attempt failed">
													</label>
													<button name="action" value="fail" type="submit">Fail</button>
												</form>
											{{end}}
											{{if .CanCollect}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<label>Amount
														<input name="amount" inputmode="decimal" value="{{.AmountDueValue}}">
													</label>
													<button name="action" value="collect" type="submit">Collect</button>
												</form>
											{{end}}
											{{if .CanRefund}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<label>Amount
														<input name="amount" inputmode="decimal" value="{{.AmountPaidValue}}">
													</label>
													<button name="action" value="refund" type="submit">Refund</button>
												</form>
											{{end}}
										</div>
									</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Invoices}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Payment History</h2>
					<span>{{len .History}} events</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.PaymentHistory}}
						<tbody>
							{{range .History}}
								<tr>
									<td>{{.InvoiceID}}</td>
									<td>{{.Transition}}</td>
									<td>{{.FromStatus}}</td>
									<td><span class="status">{{.ToStatus}}</span></td>
									<td>{{.Amount}} <small>{{.CurrencyCode}}</small></td>
									<td>{{.Reason}}</td>
									<td>{{.OccurredAt}}</td>
									<td>{{.CreatedAt}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.PaymentHistory}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Payment Setup</h2>
					<span>{{len .PaymentMethods}} methods</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table payments-table">
						{{template "ui.dense-table-head" .Tables.PaymentMethods}}
						<tbody>
							{{range .PaymentMethods}}
								<tr>
									<td>{{.PayerAccountID}}</td>
									<td><strong>{{.ProfileName}}</strong><small>{{.ProfileID}}</small></td>
									<td>{{.BillTo}}</td>
									<td><strong>{{.MethodDisplayName}}</strong><small>{{.MethodType}}</small></td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.DefaultLabel}}</td>
									<td>{{.UnappliedFunds}}</td>
									<td>{{.FailureReason}}</td>
									<td class="actions-cell">
										<div class="inline-actions">
											{{if .CanFix}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="method_id" value="{{.MethodID}}">
													<button name="action" value="fix_method" type="submit">Fix Method</button>
												</form>
											{{end}}
											{{if .CanSetDefault}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="method_id" value="{{.MethodID}}">
													<button name="action" value="set_default_method" type="submit">Set Default</button>
												</form>
											{{end}}
										</div>
									</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.PaymentMethods}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
`)
