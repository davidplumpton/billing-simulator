package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"aws-billing-simulator/internal/billingvisibility"
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

// billsFilterFromRequest normalizes bill report filters for full and partial renders.
func billsFilterFromRequest(r *http.Request) billsFilterView {
	query := r.URL.Query()
	filter := billsFilterView{
		PayerAccountID:  strings.TrimSpace(query.Get("payer_account_id")),
		UsageAccountID:  strings.TrimSpace(query.Get("usage_account_id")),
		ServiceCode:     strings.TrimSpace(query.Get("service_code")),
		ViewerRole:      strings.TrimSpace(query.Get("viewer_role")),
		ViewerAccountID: strings.TrimSpace(query.Get("viewer_account_id")),
		ApplyButton:     uiSubmitButton("Apply Filters"),
		ClearPath:       "/bills",
	}
	filter.ViewerRoleSelect = billsViewerRoleSelect(filter.ViewerRole)
	filter.ViewerAccountField = uiInputField("Viewer Account ID", "viewer_account_id", filter.ViewerAccountID, false)
	filter.HasFilters = filter.PayerAccountID != "" ||
		filter.UsageAccountID != "" ||
		filter.ServiceCode != "" ||
		filter.ViewerRole != "" ||
		filter.ViewerAccountID != ""
	return filter
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

// billsTables defines the shared dense-table metadata for bill rollups.
func billsTables() billsTablesView {
	return billsTablesView{
		BillReconciliations:      uiTable(uiTableHeaders("Bill", "Period", "Payer", "State", "Status", "Bill Items", "Source Items", "Item Delta", "Bill Total", "Source Total", "Rounding Residual", "Charge Residual", "Credit Residual", "Refund Residual", "Tax Residual", "Updated"), "No issued bills to reconcile"),
		ChargeBreakdowns:         uiTable(uiTableHeaders("Period", "Payer", "Usage Account", "Service", "Region", "Usage Type", "Status", "Resources", "Items", "Charges", "Credits", "Refunds", "Tax", "Total", "Updated"), "No charges"),
		ResourceChargeBreakdowns: uiTable(uiTableHeaders("Resource", "Period", "Payer", "Usage Account", "Service", "Region", "Usage Type", "Status", "Items", "Charges", "Credits", "Refunds", "Tax", "Total"), "No resource charges"),
		BillSummaries:            uiTable(uiTableHeaders("State", "Period", "Payer", "Items", "Charges", "Credits", "Refunds", "Tax", "Total", "Invoice", "Updated"), "No bill states"),
	}
}

// invoiceTables defines the shared dense-table metadata for printable invoices.
func invoiceTables() invoiceTablesView {
	return invoiceTablesView{
		ServiceSummaries: uiTable(uiTableHeaders("Service", "Items", "Charges", "Credits", "Refunds", "Tax", "Total"), "No service detail"),
		AccountSummaries: uiTable(uiTableHeaders("Usage Account", "Items", "Charges", "Credits", "Refunds", "Tax", "Total"), "No account detail"),
		LineItems:        uiTable(uiTableHeaders("Resource", "Account", "Service", "Type", "Region", "Usage", "Window", "Quantity", "Rate", "Cost"), "No invoice lines"),
	}
}

// invoiceActionBar returns the invoice links available for the current page state.
func invoiceActionBar(data invoicePageData, viewer exportViewerFields) uiActionBarView {
	actions := []uiActionLinkView{}
	if data.Loaded {
		actions = append(actions, uiActionLink("CSV", data.CSVPath), uiActionLink("PDF", data.PDFPath))
	}
	actions = append(actions, uiActionLink("Bills", billsPathWithExportViewer(viewer)))
	return uiActionBar(actions...)
}

// handleInvoiceCSV serves a machine-readable detailed-charge export for one invoice.
func (h billsHandler) handleInvoiceCSV(w http.ResponseWriter, r *http.Request, invoiceID string) {
	if h.db == nil {
		http.Error(w, "workspace required", http.StatusConflict)
		return
	}
	printable, err := h.invoices.GetPrintableByInvoiceID(r.Context(), invoiceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "export invoice CSV: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.ensureInvoiceViewerAccess(r.Context(), r, printable.Document.PayerAccountID); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+invoiceCSVFilename(invoiceID)+`"`)
		w.WriteHeader(http.StatusOK)
		return
	}
	body, err := invoiceCSVBytes(printable)
	if err != nil {
		http.Error(w, "export invoice CSV: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+invoiceCSVFilename(invoiceID)+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// handleInvoicePDF serves a packaged PDF rendering of the printable invoice document.
func (h billsHandler) handleInvoicePDF(w http.ResponseWriter, r *http.Request, invoiceID string) {
	if h.db == nil {
		http.Error(w, "workspace required", http.StatusConflict)
		return
	}
	printable, err := h.invoices.GetPrintableByInvoiceID(r.Context(), invoiceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "prepare invoice PDF: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.ensureInvoiceViewerAccess(r.Context(), r, printable.Document.PayerAccountID); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	viewer := exportViewerFieldsFromBillsFilter(billsFilterFromRequest(r))
	htmlPath := invoicePathForIDWithViewer(invoiceID, viewer)
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="`+invoicePDFFilename(invoiceID)+`"`)
		w.Header().Set("Link", "<"+htmlPath+`>; rel="alternate"; type="text/html"`)
		w.WriteHeader(http.StatusOK)
		return
	}
	data := invoicePageDataFromPrintable(printable, viewer)
	body, err := invoicePDFBytes(data)
	if err != nil {
		http.Error(w, "render invoice PDF: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+invoicePDFFilename(invoiceID)+`"`)
	w.Header().Set("Link", "<"+htmlPath+`>; rel="alternate"; type="text/html"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
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

// billingVisibilityFilter resolves optional simulated-viewer controls into repository account constraints.
func (h billsHandler) billingVisibilityFilter(ctx context.Context, filter billsFilterView) (persistence.BillingVisibilityFilter, error) {
	policy, scoped, err := h.billingPolicyFromFilter(ctx, filter)
	if err != nil || !scoped {
		return persistence.BillingVisibilityFilter{}, err
	}
	if !policy.AllowsView(billingvisibility.ViewBills) {
		return persistence.BillingVisibilityFilter{}, fmt.Errorf("billing role %q cannot view bills", policy.Role)
	}
	if payerAccountID, ok := policy.PayerAccountFilter(); ok {
		return persistence.BillingVisibilityFilter{PayerAccountID: payerAccountID}, nil
	}
	if usageAccountID, ok := policy.UsageAccountFilter(); ok {
		return persistence.BillingVisibilityFilter{UsageAccountID: usageAccountID}, nil
	}
	return persistence.BillingVisibilityFilter{}, nil
}

// billingPolicyFromFilter builds the domain visibility policy from explicit viewer query fields.
func (h billsHandler) billingPolicyFromFilter(ctx context.Context, filter billsFilterView) (billingvisibility.Policy, bool, error) {
	roleValue := strings.TrimSpace(filter.ViewerRole)
	accountID := strings.TrimSpace(filter.ViewerAccountID)
	if roleValue == "" && accountID == "" {
		return billingvisibility.Policy{}, false, nil
	}
	if roleValue == "" {
		return billingvisibility.Policy{}, false, fmt.Errorf("viewer role is required when viewer account ID is set")
	}
	role, err := billingvisibility.ParseRole(roleValue)
	if err != nil {
		return billingvisibility.Policy{}, false, err
	}
	managementAccountID, err := defaultBillingPayerAccountID(ctx, h.db, "")
	if err != nil {
		return billingvisibility.Policy{}, false, err
	}
	if role == billingvisibility.RoleManagementAccount && accountID == "" {
		accountID = managementAccountID
	}
	if role == billingvisibility.RoleFinance && accountID == "" {
		accountID = managementAccountID
	}
	policy, err := billingvisibility.PolicyForViewer(billingvisibility.Viewer{
		Role:                role,
		AccountID:           accountID,
		ManagementAccountID: managementAccountID,
	})
	if err != nil {
		return billingvisibility.Policy{}, false, err
	}
	return policy, true, nil
}

// invoiceAccessPredicate separates bill-row visibility from financial document access.
func (h billsHandler) invoiceAccessPredicate(ctx context.Context, filter billsFilterView) (func(string) bool, error) {
	policy, scoped, err := h.billingPolicyFromFilter(ctx, filter)
	if err != nil {
		return nil, err
	}
	if !scoped {
		return func(string) bool { return true }, nil
	}
	return func(payerAccountID string) bool {
		return policy.AllowsView(billingvisibility.ViewInvoices) && policy.AllowsPayerAccount(strings.TrimSpace(payerAccountID))
	}, nil
}

// ensureInvoiceViewerAccess enforces financial-document access when a simulated viewer is selected.
func (h billsHandler) ensureInvoiceViewerAccess(ctx context.Context, r *http.Request, payerAccountID string) error {
	policy, scoped, err := h.billingPolicyFromFilter(ctx, billsFilterFromRequest(r))
	if err != nil || !scoped {
		return err
	}
	if !policy.AllowsView(billingvisibility.ViewInvoices) {
		return fmt.Errorf("billing role %q cannot view invoices", policy.Role)
	}
	if payerAccountID != "" && !policy.AllowsPayerAccount(payerAccountID) {
		return fmt.Errorf("billing role %q cannot view invoice for payer account %q", policy.Role, payerAccountID)
	}
	return nil
}

func filterBillStateSummaries(rows []persistence.BillStateSummary, filter billsFilterView) []persistence.BillStateSummary {
	filtered := make([]persistence.BillStateSummary, 0, len(rows))
	for _, row := range rows {
		if matchesFilter(row.PayerAccountID, filter.PayerAccountID) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterBillReconciliations(rows []persistence.BillReconciliation, filter billsFilterView) []persistence.BillReconciliation {
	filtered := make([]persistence.BillReconciliation, 0, len(rows))
	for _, row := range rows {
		if matchesFilter(row.PayerAccountID, filter.PayerAccountID) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterBillChargeSummaries(rows []persistence.BillChargeSummary, filter billsFilterView) []persistence.BillChargeSummary {
	filtered := make([]persistence.BillChargeSummary, 0, len(rows))
	for _, row := range rows {
		if matchesFilter(row.PayerAccountID, filter.PayerAccountID) &&
			matchesFilter(row.UsageAccountID, filter.UsageAccountID) &&
			matchesFilter(row.ServiceCode, filter.ServiceCode) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterBillResourceChargeSummaries(rows []persistence.BillResourceChargeSummary, filter billsFilterView) []persistence.BillResourceChargeSummary {
	filtered := make([]persistence.BillResourceChargeSummary, 0, len(rows))
	for _, row := range rows {
		if matchesFilter(row.PayerAccountID, filter.PayerAccountID) &&
			matchesFilter(row.UsageAccountID, filter.UsageAccountID) &&
			matchesFilter(row.ServiceCode, filter.ServiceCode) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func limitedTableLabel(displayed, total int, singular, plural string) string {
	if total < displayed {
		total = displayed
	}
	if total > displayed {
		return fmt.Sprintf("%d of %d %s shown", displayed, total, plural)
	}
	noun := plural
	if displayed == 1 {
		noun = singular
	}
	return fmt.Sprintf("%d %s", displayed, noun)
}

func billSummaryViewFromSummary(summary persistence.BillStateSummary, filter billsFilterView, canViewInvoice bool) billSummaryView {
	invoicePath := ""
	invoiceCSVPath := ""
	invoicePDFPath := ""
	curCSVPath := ""
	curReconcilePath := ""
	invoiceID := strings.TrimSpace(summary.InvoiceID)
	hasIssuedBill := invoiceID != "" || billStateHasInvoiceDocument(summary.BillState)
	if hasIssuedBill {
		viewer := exportViewerFieldsFromBillsFilter(filter)
		usageAccountID := strings.TrimSpace(filter.UsageAccountID)
		if viewer.Role == billingvisibility.RoleMemberAccount.String() && viewer.AccountID != "" {
			usageAccountID = viewer.AccountID
		}
		if invoiceID != "" && canViewInvoice {
			invoicePath = invoicePathForIDWithViewer(summary.InvoiceID, viewer)
			invoiceCSVPath = invoiceCSVPathForIDWithViewer(summary.InvoiceID, viewer)
			invoicePDFPath = invoicePDFPathForIDWithViewer(summary.InvoiceID, viewer)
		}
		curCSVPath = curCSVExportPathWithViewer(persistence.CURCSVExportRequest{
			BillingPeriodStart: summary.BillingPeriodStart,
			BillingPeriodEnd:   summary.BillingPeriodEnd,
			PayerAccountID:     summary.PayerAccountID,
			UsageAccountID:     usageAccountID,
			LineItemStatus:     "final",
		}, viewer)
		curReconcilePath = curExportReconciliationPathWithViewer(persistence.CURExportReconciliationRequest{
			BillingPeriodStart: summary.BillingPeriodStart,
			BillingPeriodEnd:   summary.BillingPeriodEnd,
			PayerAccountID:     summary.PayerAccountID,
			UsageAccountID:     usageAccountID,
			LineItemStatus:     "final",
		}, viewer)
	}
	return billSummaryView{
		ID:                summary.ID,
		Period:            summary.BillingPeriodStart + " to " + summary.BillingPeriodEnd,
		PayerAccountID:    summary.PayerAccountID,
		State:             displayBillState(summary.BillState),
		LineItemCount:     summary.LineItemCount,
		Charges:           formatUSDMicros(summary.UsageChargeMicros),
		Credits:           formatUSDMicros(summary.CreditMicros),
		Refunds:           formatUSDMicros(summary.RefundMicros),
		Tax:               formatUSDMicros(summary.TaxMicros),
		Total:             formatUSDMicros(summary.TotalMicros),
		InvoiceID:         summary.InvoiceID,
		InvoicePath:       invoicePath,
		InvoiceCSVPath:    invoiceCSVPath,
		InvoicePDFPath:    invoicePDFPath,
		CanViewInvoice:    canViewInvoice,
		InvoiceRestricted: hasIssuedBill && !canViewInvoice,
		CURCSVPath:        curCSVPath,
		CURReconcilePath:  curReconcilePath,
		InvoiceStatus:     displayBillState(summary.InvoiceStatus),
		InvoiceAmountDue:  formatUSDMicros(summary.InvoiceAmountDueMicros),
		InvoicePaid:       formatUSDMicros(summary.InvoiceAmountPaidMicros),
		InvoiceDate:       summary.InvoiceDate,
		InvoiceDueDate:    summary.InvoiceDueDate,
		UpdatedAt:         summary.UpdatedAt,
	}
}

// invoicePageDataFromPrintable prepares a printable invoice for the HTML template.
func invoicePageDataFromPrintable(printable persistence.PrintableInvoice, viewer exportViewerFields) invoicePageData {
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
		CSVPath:               invoiceCSVPathForIDWithViewer(document.InvoiceID, viewer),
		PDFPath:               invoicePDFPathForIDWithViewer(document.InvoiceID, viewer),
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

// invoiceCSVBytes writes the detailed invoice line-item export using the printable invoice read model.
func invoiceCSVBytes(printable persistence.PrintableInvoice) ([]byte, error) {
	var body bytes.Buffer
	writer := csv.NewWriter(&body)
	if err := writer.Write(invoiceCSVHeader()); err != nil {
		return nil, err
	}
	for _, item := range printable.LineItems {
		if err := writer.Write(invoiceCSVRecord(printable, item)); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

// invoiceCSVHeader returns stable column names for the invoice detailed-charge export.
func invoiceCSVHeader() []string {
	return []string{
		"invoice_id",
		"bill_id",
		"document_status",
		"payment_status",
		"billing_period_start",
		"billing_period_end",
		"invoice_date",
		"due_date",
		"payer_account_id",
		"usage_account_id",
		"line_item_id",
		"line_item_type",
		"service_code",
		"service_name",
		"region_code",
		"resource_id",
		"resource_name",
		"usage_type",
		"operation",
		"usage_start_time",
		"usage_end_time",
		"pricing_quantity",
		"pricing_unit",
		"unblended_rate",
		"unblended_cost",
		"currency_code",
		"description",
	}
}

// invoiceCSVRecord formats one invoice source line item as a CSV row.
func invoiceCSVRecord(printable persistence.PrintableInvoice, item persistence.InvoiceLineItem) []string {
	document := printable.Document
	obligation := printable.Obligation
	return []string{
		document.InvoiceID,
		document.BillID,
		document.Status,
		obligation.Status,
		document.BillingPeriodStart,
		document.BillingPeriodEnd,
		document.InvoiceDate,
		document.DueDate,
		document.PayerAccountID,
		item.UsageAccountID,
		item.ID,
		item.LineItemType,
		item.ServiceCode,
		item.ServiceName,
		item.RegionCode,
		item.ResourceID,
		item.ResourceName,
		item.UsageType,
		item.Operation,
		item.UsageStartTime,
		item.UsageEndTime,
		formatMicrosDecimal(item.PricingQuantityMicros),
		item.PricingUnit,
		formatMicrosDecimal(item.UnblendedRateMicros),
		formatMicrosDecimal(item.UnblendedCostMicros),
		item.CurrencyCode,
		item.Description,
	}
}

// formatMicrosDecimal renders micros as a fixed six-decimal value for CSV readers.
func formatMicrosDecimal(value int64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	return fmt.Sprintf("%s%d.%06d", sign, value/1_000_000, value%1_000_000)
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

func billStateHasInvoiceDocument(state string) bool {
	switch strings.TrimSpace(state) {
	case "issued", "adjusted", "paid", "past_due":
		return true
	default:
		return false
	}
}

func displayBillState(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ReplaceAll(value, "_", "-")
}

// invoiceRouteFromPath extracts the invoice ID and optional export kind from an invoice URL path.
func invoiceRouteFromPath(path string) (invoiceRoute, bool) {
	const prefix = "/invoices/"
	if !strings.HasPrefix(path, prefix) {
		return invoiceRoute{}, false
	}
	rawID := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	export := invoiceExportHTML
	if strings.HasSuffix(rawID, invoiceCSVPathSuffix) {
		rawID = strings.TrimSuffix(rawID, invoiceCSVPathSuffix)
		export = invoiceExportCSV
	} else if strings.HasSuffix(rawID, invoicePDFPathSuffix) {
		rawID = strings.TrimSuffix(rawID, invoicePDFPathSuffix)
		export = invoiceExportPDF
	}
	if rawID == "" || strings.Contains(rawID, "/") {
		return invoiceRoute{}, false
	}
	invoiceID, err := url.PathUnescape(rawID)
	if err != nil || strings.TrimSpace(invoiceID) == "" {
		return invoiceRoute{}, false
	}
	return invoiceRoute{InvoiceID: strings.TrimSpace(invoiceID), Export: export}, true
}

// invoiceIDFromPath extracts and unescapes the invoice ID from /invoices/{id}.
func invoiceIDFromPath(path string) (string, bool) {
	route, ok := invoiceRouteFromPath(path)
	if !ok || route.Export != invoiceExportHTML {
		return "", false
	}
	return route.InvoiceID, true
}

// invoicePathForID escapes an invoice ID for use as an internal invoice link.
func invoicePathForID(invoiceID string) string {
	return "/invoices/" + url.PathEscape(strings.TrimSpace(invoiceID))
}

func invoicePathForIDWithViewer(invoiceID string, viewer exportViewerFields) string {
	return invoicePathWithViewer(invoicePathForID(invoiceID), viewer)
}

// invoiceCSVPathForID returns the detailed-charge CSV download URL for an invoice.
func invoiceCSVPathForID(invoiceID string) string {
	return invoicePathForID(invoiceID) + invoiceCSVPathSuffix
}

func invoiceCSVPathForIDWithViewer(invoiceID string, viewer exportViewerFields) string {
	return invoicePathWithViewer(invoiceCSVPathForID(invoiceID), viewer)
}

// invoicePDFPathForID returns the packaged-PDF download URL for an invoice.
func invoicePDFPathForID(invoiceID string) string {
	return invoicePathForID(invoiceID) + invoicePDFPathSuffix
}

func invoicePDFPathForIDWithViewer(invoiceID string, viewer exportViewerFields) string {
	return invoicePathWithViewer(invoicePDFPathForID(invoiceID), viewer)
}

func invoicePathWithViewer(path string, viewer exportViewerFields) string {
	values := url.Values{}
	viewer.appendToValues(values)
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}

// invoiceCSVFilename sanitizes invoice IDs for the CSV content-disposition filename.
func invoiceCSVFilename(invoiceID string) string {
	return invoiceSafeFilenameBase(invoiceID) + "-line-items.csv"
}

// invoicePDFFilename sanitizes invoice IDs for the PDF content-disposition filename.
func invoicePDFFilename(invoiceID string) string {
	return invoiceSafeFilenameBase(invoiceID) + "-document.pdf"
}

func invoiceSafeFilenameBase(invoiceID string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(invoiceID) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('-')
		}
	}
	safe := strings.Trim(builder.String(), "-")
	if safe == "" {
		safe = "invoice"
	}
	return safe
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
