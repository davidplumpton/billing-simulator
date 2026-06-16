package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"aws-billing-simulator/internal/billingvisibility"
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
	Filters             paymentFilterView
	BillsPath           string
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
	ViewerRole          string
	ViewerAccountID     string
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
	ViewerRole        string
	ViewerAccountID   string
}

type paymentsTablesView struct {
	Invoices       uiTableView
	PaymentHistory uiTableView
	PaymentMethods uiTableView
}

type paymentFilterView struct {
	ViewerRole         string
	ViewerAccountID    string
	ViewerRoleField    uiSelectFieldView
	ViewerAccountField uiInputFieldView
	ApplyButton        uiSubmitButtonView
	ClearPath          string
	HasFilters         bool
}

// paymentAccessError marks payment-policy denials so handlers can return 403.
type paymentAccessError struct {
	err error
}

func (e paymentAccessError) Error() string {
	return e.err.Error()
}

func (e paymentAccessError) Unwrap() error {
	return e.err
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
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	h.renderPaymentsForValues(w, r, http.StatusOK, "", flashFromQuery(r), r.URL.Query())
}

// handlePaymentAction applies one simulated invoice or profile payment action.
func (h paymentsHandler) handlePaymentAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
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
	viewer := exportViewerFieldsFromValues(r.PostForm)
	flash, err := paymentActionRunnerFromHandler(h).Apply(r.Context(), r.PostForm)
	if err != nil {
		h.renderPaymentsForValues(w, r, paymentHTTPStatus(err), err.Error(), "", r.PostForm)
		return
	}
	http.Redirect(w, r, paymentsPathWithViewer(viewer, flash), http.StatusSeeOther)
}

func (h paymentsHandler) renderPayments(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	h.renderPaymentsForValues(w, r, status, errorMessage, flashMessage, r.URL.Query())
}

// renderPaymentsForValues renders the Payments page using either GET query values or POST form values.
func (h paymentsHandler) renderPaymentsForValues(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string, values url.Values) {
	viewer := exportViewerFieldsFromValues(values)
	data := paymentsPageData{
		WorkspaceReady:      h.db != nil,
		Flash:               flashMessage,
		Error:               errorMessage,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Filters:             paymentFilterFromValues(values),
		BillsPath:           billsPathWithExportViewer(viewer),
		Tables:              paymentsTables(),
	}
	if h.db != nil {
		policy, err := h.paymentPolicyFromValues(r.Context(), values)
		if err != nil {
			status = paymentHTTPStatus(err)
			if data.Error == "" {
				data.Error = "list payments: " + err.Error()
			}
		} else if err := h.loadPaymentsPageData(r.Context(), &data, policy, viewer); err != nil {
			status = http.StatusInternalServerError
			if data.Error == "" {
				data.Error = err.Error()
			}
		}
	}
	data.Notices = uiNotices(data.Flash, data.Error)

	renderPage(w, status, pageLayoutOptions{
		Title:     "Payments - Billing Simulator",
		ActiveNav: "payments",
	}, paymentsPageTemplate, data, "render payments page")
}

// loadPaymentsPageData assembles invoices, lifecycle history, and profile setup for payment workflows.
func (h paymentsHandler) loadPaymentsPageData(ctx context.Context, data *paymentsPageData, policy billingvisibility.Policy, viewer exportViewerFields) error {
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
	issuedBills = paymentIssuedBillsVisibleToPolicy(issuedBills, policy)
	data.StateCards = paymentStateCards(issuedBills)
	defaultPayerAccountID, err := defaultBillingPayerAccountID(ctx, h.db, "")
	if err != nil {
		return err
	}
	payerIDs := paymentVisiblePayerIDs(defaultPayerAccountID, issuedBills, policy)

	for _, issued := range issuedBills {
		events, err := h.lifecycle.ListEvents(ctx, issued.Obligation.ID, 10)
		if err != nil {
			return err
		}
		failureReason := recentFailureReason(events)
		if issued.Obligation.AmountDueMicros > 0 || issued.Obligation.Status != "succeeded" {
			data.Invoices = append(data.Invoices, paymentInvoiceViewFromIssuedBill(issued, failureReason, viewer))
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
				data.PaymentMethods = append(data.PaymentMethods, paymentMethodRowViewFromProfile(profile, viewer))
				continue
			}
			for _, method := range methods {
				data.PaymentMethods = append(data.PaymentMethods, paymentMethodRowViewFromMethod(profile, method, viewer))
			}
		}
	}
	return nil
}

// paymentPolicyFromValues resolves viewer controls into the policy allowed to manage payments.
func (h paymentsHandler) paymentPolicyFromValues(ctx context.Context, values url.Values) (billingvisibility.Policy, error) {
	return paymentPolicyFromValues(ctx, h.db, values)
}

func paymentPolicyFromValues(ctx context.Context, db *sql.DB, values url.Values) (billingvisibility.Policy, error) {
	resolution, err := resolveViewerPolicy(ctx, db, exportViewerFieldsFromValues(values), viewerPolicyResolveOptions{
		DefaultRole:  billingvisibility.RoleManagementAccount,
		RequiredView: billingvisibility.ViewPayments,
		PermissionErr: func(policy billingvisibility.Policy) error {
			return paymentAccessError{err: fmt.Errorf("billing role %q cannot manage payments", policy.Role)}
		},
	})
	if err != nil {
		return billingvisibility.Policy{}, err
	}
	return resolution.Policy, nil
}

// paymentHTTPStatus maps payment-policy errors to user-facing HTTP statuses.
func paymentHTTPStatus(err error) int {
	var accessErr paymentAccessError
	if err != nil && errors.As(err, &accessErr) {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

// paymentIssuedBillsVisibleToPolicy filters invoice rows to payers the policy can manage.
func paymentIssuedBillsVisibleToPolicy(issuedBills []persistence.BillWithInvoiceObligation, policy billingvisibility.Policy) []persistence.BillWithInvoiceObligation {
	filtered := make([]persistence.BillWithInvoiceObligation, 0, len(issuedBills))
	for _, issued := range issuedBills {
		if policy.AllowsPayerAccount(issued.Bill.PayerAccountID) {
			filtered = append(filtered, issued)
		}
	}
	return filtered
}

// paymentVisiblePayerIDs chooses payer profiles shown for the active payment policy.
func paymentVisiblePayerIDs(defaultPayerAccountID string, issuedBills []persistence.BillWithInvoiceObligation, policy billingvisibility.Policy) map[string]bool {
	payerIDs := map[string]bool{}
	if payerAccountID, ok := policy.PayerAccountFilter(); ok {
		payerIDs[payerAccountID] = true
		return payerIDs
	}
	if policy.AccountScope == billingvisibility.AccountScopeAllAccounts {
		if defaultPayerAccountID != "" {
			payerIDs[defaultPayerAccountID] = true
		}
		for _, issued := range issuedBills {
			if strings.TrimSpace(issued.Bill.PayerAccountID) != "" {
				payerIDs[issued.Bill.PayerAccountID] = true
			}
		}
	}
	return payerIDs
}

// paymentFilterFromValues builds the simulated viewer selector state for Payments.
func paymentFilterFromValues(values url.Values) paymentFilterView {
	filter := paymentFilterView{
		ViewerRole:      strings.TrimSpace(values.Get("viewer_role")),
		ViewerAccountID: strings.TrimSpace(values.Get("viewer_account_id")),
		ApplyButton:     uiSubmitButton("Apply"),
		ClearPath:       "/payments",
	}
	filter.ViewerRoleField = viewerRoleSelectField(filter.ViewerRole, "Default viewer")
	filter.ViewerAccountField = viewerAccountIDField(filter.ViewerAccountID)
	filter.HasFilters = filter.ViewerRole != "" || filter.ViewerAccountID != ""
	return filter
}

func paymentsTables() paymentsTablesView {
	return paymentsTablesView{
		Invoices:       uiTable(uiTableHeaders("Invoice", "Period", "Payer", "Bill", "Payment State", "Due", "Paid", "Due Date", "Failure", "Actions"), "No due invoices"),
		PaymentHistory: uiTable(uiTableHeaders("Invoice", "Transition", "From", "To", "Amount", "Reason", "Occurred", "Recorded"), "No payment history"),
		PaymentMethods: uiTable(uiTableHeaders("Payer", "Profile", "Bill To", "Method", "Status", "Default", "Unapplied Funds", "Failure", "Actions"), "No payment profiles"),
	}
}

func paymentInvoiceViewFromIssuedBill(issued persistence.BillWithInvoiceObligation, failureReason string, viewer exportViewerFields) paymentInvoiceView {
	obligation := issued.Obligation
	status := strings.TrimSpace(obligation.Status)
	return paymentInvoiceView{
		InvoiceObligationID: obligation.ID,
		InvoiceID:           obligation.InvoiceID,
		InvoicePath:         invoicePathForIDWithViewer(obligation.InvoiceID, viewer),
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
		ViewerRole:          viewer.Role,
		ViewerAccountID:     viewer.AccountID,
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

func paymentMethodRowViewFromProfile(profile persistence.PaymentProfile, viewer exportViewerFields) paymentMethodRowView {
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
		ViewerRole:        viewer.Role,
		ViewerAccountID:   viewer.AccountID,
	}
}

func paymentMethodRowViewFromMethod(profile persistence.PaymentProfile, method persistence.PaymentMethod, viewer exportViewerFields) paymentMethodRowView {
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
		ViewerRole:        viewer.Role,
		ViewerAccountID:   viewer.AccountID,
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
	return amountDueMicros > 0 && matchesAny(status, "scheduled", "failed", "past_due", "partially_paid", "refunded")
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

func paymentPastDueDate(dueDate string) string {
	parsed, err := time.Parse(time.DateOnly, strings.TrimSpace(dueDate))
	if err != nil {
		return dueDate
	}
	return parsed.AddDate(0, 0, 1).Format(time.DateOnly)
}
