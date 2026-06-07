package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
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
		methodNotAllowed(w)
		return
	}
	h.renderPaymentsForValues(w, r, http.StatusOK, "", flashFromQuery(r), r.URL.Query())
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
	viewer := exportViewerFieldsFromValues(r.PostForm)
	policy, err := h.paymentPolicyFromValues(r.Context(), r.PostForm)
	if err != nil {
		h.renderPaymentsForValues(w, r, paymentHTTPStatus(err), "payment action: "+err.Error(), "", r.PostForm)
		return
	}
	flash, err := h.applyPaymentAction(r.Context(), r, policy)
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
		Title:     "Payments - AWS Billing Simulator",
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

func (h paymentsHandler) applyPaymentAction(ctx context.Context, r *http.Request, policy billingvisibility.Policy) (string, error) {
	action := strings.TrimSpace(r.PostForm.Get("action"))
	switch action {
	case "schedule":
		request, err := h.visibleTransitionRequestFromForm(ctx, r, policy, false)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.SchedulePayment(ctx, request)
		if err != nil {
			return "", err
		}
		return "Scheduled payment for " + result.Obligation.InvoiceID, nil
	case "process":
		request, err := h.visibleTransitionRequestFromForm(ctx, r, policy, false)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.StartProcessing(ctx, request)
		if err != nil {
			return "", err
		}
		return "Started payment processing for " + result.Obligation.InvoiceID, nil
	case "fail":
		request, err := h.visibleTransitionRequestFromForm(ctx, r, policy, false)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.FailPayment(ctx, request)
		if err != nil {
			return "", err
		}
		return "Recorded failed payment for " + result.Obligation.InvoiceID, nil
	case "mark_due":
		request, err := h.visibleTransitionRequestFromForm(ctx, r, policy, false)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.MarkDue(ctx, request)
		if err != nil {
			return "", err
		}
		return "Marked " + result.Obligation.InvoiceID + " due", nil
	case "mark_past_due":
		request, err := h.visibleTransitionRequestFromForm(ctx, r, policy, false)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.MarkPastDue(ctx, request)
		if err != nil {
			return "", err
		}
		return "Marked " + result.Obligation.InvoiceID + " past due", nil
	case "collect":
		request, err := h.visibleTransitionRequestFromForm(ctx, r, policy, true)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.ApplyPayment(ctx, request)
		if err != nil {
			return "", err
		}
		return "Collected payment for " + result.Obligation.InvoiceID, nil
	case "refund":
		request, err := h.visibleTransitionRequestFromForm(ctx, r, policy, true)
		if err != nil {
			return "", err
		}
		result, err := h.lifecycle.RefundPayment(ctx, request)
		if err != nil {
			return "", err
		}
		return "Recorded refund for " + result.Obligation.InvoiceID, nil
	case "fix_method":
		if _, err := h.ensurePaymentMethodVisible(ctx, policy, r.PostForm.Get("method_id")); err != nil {
			return "", err
		}
		method, err := h.profiles.ResolvePaymentMethodFailure(ctx, r.PostForm.Get("method_id"))
		if err != nil {
			return "", err
		}
		return "Fixed payment method " + method.DisplayName, nil
	case "set_default_method":
		if _, err := h.ensurePaymentMethodVisible(ctx, policy, r.PostForm.Get("method_id")); err != nil {
			return "", err
		}
		method, err := h.profiles.SetDefaultPaymentMethod(ctx, r.PostForm.Get("method_id"))
		if err != nil {
			return "", err
		}
		return "Selected default payment method " + method.DisplayName, nil
	case "set_default_profile":
		if _, err := h.ensurePaymentProfileVisible(ctx, policy, r.PostForm.Get("profile_id")); err != nil {
			return "", err
		}
		profile, err := h.profiles.SetDefaultPaymentProfile(ctx, r.PostForm.Get("profile_id"))
		if err != nil {
			return "", err
		}
		return "Selected default payment profile " + profile.ProfileName, nil
	default:
		return "", fmt.Errorf("unsupported payment action %q", action)
	}
}

// visibleTransitionRequestFromForm validates one invoice transition against the active payment policy.
func (h paymentsHandler) visibleTransitionRequestFromForm(ctx context.Context, r *http.Request, policy billingvisibility.Policy, includeAmount bool) (persistence.PaymentLifecycleTransitionRequest, error) {
	request, err := h.transitionRequestFromForm(ctx, r, includeAmount)
	if err != nil {
		return persistence.PaymentLifecycleTransitionRequest{}, err
	}
	if err := h.ensurePaymentObligationVisible(ctx, policy, request.InvoiceObligationID); err != nil {
		return persistence.PaymentLifecycleTransitionRequest{}, err
	}
	return request, nil
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

// paymentPolicyFromValues resolves viewer controls into the policy allowed to manage payments.
func (h paymentsHandler) paymentPolicyFromValues(ctx context.Context, values url.Values) (billingvisibility.Policy, error) {
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
	if !policy.AllowsView(billingvisibility.ViewPayments) {
		return billingvisibility.Policy{}, paymentAccessError{err: fmt.Errorf("billing role %q cannot manage payments", policy.Role)}
	}
	return policy, nil
}

// paymentHTTPStatus maps payment-policy errors to user-facing HTTP statuses.
func paymentHTTPStatus(err error) int {
	var accessErr paymentAccessError
	if err != nil && errors.As(err, &accessErr) {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

// ensurePaymentObligationVisible checks the payer for an invoice obligation before mutation.
func (h paymentsHandler) ensurePaymentObligationVisible(ctx context.Context, policy billingvisibility.Policy, invoiceObligationID string) error {
	payerAccountID, err := h.paymentObligationPayerAccountID(ctx, invoiceObligationID)
	if err != nil {
		return err
	}
	return ensurePaymentPayerVisible(policy, payerAccountID)
}

// paymentObligationPayerAccountID reads the payer account attached to an invoice obligation.
func (h paymentsHandler) paymentObligationPayerAccountID(ctx context.Context, invoiceObligationID string) (string, error) {
	invoiceObligationID = strings.TrimSpace(invoiceObligationID)
	if invoiceObligationID == "" {
		return "", fmt.Errorf("invoice obligation ID is required")
	}
	var payerAccountID string
	if err := h.db.QueryRowContext(
		ctx,
		`SELECT b.payer_account_id
		   FROM invoice_obligations o
		   JOIN bills b ON b.id = o.bill_id
		  WHERE o.id = ?`,
		invoiceObligationID,
	).Scan(&payerAccountID); err != nil {
		return "", fmt.Errorf("get invoice obligation payer %q: %w", invoiceObligationID, err)
	}
	return payerAccountID, nil
}

// ensurePaymentMethodVisible checks a payment method's parent profile against the active policy.
func (h paymentsHandler) ensurePaymentMethodVisible(ctx context.Context, policy billingvisibility.Policy, methodID string) (persistence.PaymentMethod, error) {
	method, err := h.profiles.GetPaymentMethod(ctx, methodID)
	if err != nil {
		return persistence.PaymentMethod{}, err
	}
	if _, err := h.ensurePaymentProfileVisible(ctx, policy, method.PaymentProfileID); err != nil {
		return persistence.PaymentMethod{}, err
	}
	return method, nil
}

// ensurePaymentProfileVisible checks a payment profile payer against the active policy.
func (h paymentsHandler) ensurePaymentProfileVisible(ctx context.Context, policy billingvisibility.Policy, profileID string) (persistence.PaymentProfile, error) {
	profile, err := h.profiles.GetPaymentProfile(ctx, profileID)
	if err != nil {
		return persistence.PaymentProfile{}, err
	}
	if err := ensurePaymentPayerVisible(policy, profile.PayerAccountID); err != nil {
		return persistence.PaymentProfile{}, err
	}
	return profile, nil
}

// ensurePaymentPayerVisible centralizes payer-level payment authorization.
func ensurePaymentPayerVisible(policy billingvisibility.Policy, payerAccountID string) error {
	if policy.AllowsPayerAccount(payerAccountID) {
		return nil
	}
	return paymentAccessError{err: fmt.Errorf("billing role %q cannot manage payments for payer account %q", policy.Role, strings.TrimSpace(payerAccountID))}
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
	filter.ViewerRoleField = exportsViewerRoleSelect(filter.ViewerRole)
	filter.ViewerAccountField = uiInputField("Viewer Account ID", "viewer_account_id", filter.ViewerAccountID, false)
	filter.HasFilters = filter.ViewerRole != "" || filter.ViewerAccountID != ""
	return filter
}

// paymentsPathWithViewer preserves simulated viewer fields after payment actions.
func paymentsPathWithViewer(viewer exportViewerFields, flash string) string {
	values := url.Values{}
	viewer.appendToValues(values)
	appendQueryValue(values, "flash", flash)
	encoded := values.Encode()
	if encoded == "" {
		return "/payments"
	}
	return "/payments?" + encoded
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
				<a class="button-link secondary" href="{{.BillsPath}}">Bills</a>
			</div>
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section>
				<form method="get" action="/payments" class="filter-form">
					{{template "ui.select-field" .Filters.ViewerRoleField}}
					{{template "ui.input-field" .Filters.ViewerAccountField}}
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
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<button name="action" value="schedule" type="submit">Schedule</button>
												</form>
											{{end}}
											{{if .CanProcess}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<button name="action" value="process" type="submit">Retry</button>
												</form>
											{{end}}
											{{if .CanMarkDue}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<button name="action" value="mark_due" type="submit">Mark Due</button>
												</form>
											{{end}}
											{{if .CanMarkPastDue}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<label>As Of
														<input type="date" name="occurred_at" value="{{.PastDueDate}}">
													</label>
													<button name="action" value="mark_past_due" type="submit">Past Due</button>
												</form>
											{{end}}
											{{if .CanFail}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<label>Reason
														<input name="reason" value="Payment attempt failed">
													</label>
													<button name="action" value="fail" type="submit">Fail</button>
												</form>
											{{end}}
											{{if .CanCollect}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<label>Amount
														<input name="amount" inputmode="decimal" value="{{.AmountDueValue}}">
													</label>
													<button name="action" value="collect" type="submit">Collect</button>
												</form>
											{{end}}
											{{if .CanRefund}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
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
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<button name="action" value="fix_method" type="submit">Fix Method</button>
												</form>
											{{end}}
											{{if .CanSetDefault}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="method_id" value="{{.MethodID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
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
