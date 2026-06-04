package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
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
	InvoiceCSVPath   string
	InvoicePDFPath   string
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
	CSVPath               string
	PDFPath               string
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
	route, ok := invoiceRouteFromPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch route.Export {
	case invoiceExportCSV:
		h.handleInvoiceCSV(w, r, route.InvoiceID)
	case invoiceExportPDF:
		h.handleInvoicePDFPlan(w, r, route.InvoiceID)
	default:
		h.renderInvoice(w, r, http.StatusOK, route.InvoiceID, "")
	}
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

	renderPage(w, status, pageLayoutOptions{
		Title:     "Invoice " + data.InvoiceID + " - AWS Billing Simulator",
		ActiveNav: "bills",
		MainClass: "invoice-page",
	}, invoicePageTemplate, data, "render invoice page")
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

// handleInvoicePDFPlan reserves the packaged PDF URL and points it at the printable HTML source.
func (h billsHandler) handleInvoicePDFPlan(w http.ResponseWriter, r *http.Request, invoiceID string) {
	if h.db == nil {
		http.Error(w, "workspace required", http.StatusConflict)
		return
	}
	if _, err := h.invoices.GetPrintableByInvoiceID(r.Context(), invoiceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "prepare invoice PDF: "+err.Error(), http.StatusInternalServerError)
		return
	}

	htmlPath := invoicePathForID(invoiceID)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Link", "<"+htmlPath+`>; rel="alternate"; type="text/html"`)
	w.Header().Set("X-Invoice-PDF-Implementation", "packaged-html-to-pdf")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = fmt.Fprintf(w, "PDF export for invoice %s is reserved for the packaged HTML-to-PDF renderer. Printable HTML is available at %s.\n", invoiceID, htmlPath)
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
	summaries, err := h.bills.ListBillStateSummaries(ctx, persistence.BillStateSummaryRequest{
		Limit:                 50,
		DefaultPayerAccountID: defaultPayerAccountID,
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
	invoiceCSVPath := ""
	invoicePDFPath := ""
	if strings.TrimSpace(summary.InvoiceID) != "" {
		invoicePath = invoicePathForID(summary.InvoiceID)
		invoiceCSVPath = invoiceCSVPathForID(summary.InvoiceID)
		invoicePDFPath = invoicePDFPathForID(summary.InvoiceID)
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
		InvoiceCSVPath:   invoiceCSVPath,
		InvoicePDFPath:   invoicePDFPath,
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
		CSVPath:               invoiceCSVPathForID(document.InvoiceID),
		PDFPath:               invoicePDFPathForID(document.InvoiceID),
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

// invoiceCSVPathForID returns the detailed-charge CSV download URL for an invoice.
func invoiceCSVPathForID(invoiceID string) string {
	return invoicePathForID(invoiceID) + invoiceCSVPathSuffix
}

// invoicePDFPathForID returns the reserved packaged-PDF URL for an invoice.
func invoicePDFPathForID(invoiceID string) string {
	return invoicePathForID(invoiceID) + invoicePDFPathSuffix
}

// invoiceCSVFilename sanitizes invoice IDs for the CSV content-disposition filename.
func invoiceCSVFilename(invoiceID string) string {
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
	return safe + "-line-items.csv"
}

var billsPageTemplate = template.Must(template.New("bills-page").Parse(`<div class="page-heading">
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
											<small><a href="{{.InvoiceCSVPath}}">CSV</a> <a href="{{.InvoicePDFPath}}">PDF</a></small>
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
`))

var invoicePageTemplate = template.Must(template.New("invoice-page").Parse(`<div class="page-heading">
			<div>
				<h1>Invoice {{.InvoiceID}}</h1>
			</div>
			<div class="page-actions">
				{{if .Loaded}}
					<a class="button-link" href="{{.CSVPath}}">CSV</a>
					<a class="button-link" href="{{.PDFPath}}">PDF</a>
				{{end}}
				<a class="button-link" href="/bills">Bills</a>
			</div>
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
`))
