package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"aws-billing-simulator/internal/billingvisibility"
	"aws-billing-simulator/internal/persistence"
)

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
	filter.ViewerRoleSelect = viewerRoleSelectField(filter.ViewerRole, "All viewers")
	filter.ViewerAccountField = viewerAccountIDField(filter.ViewerAccountID)
	filter.HasFilters = filter.PayerAccountID != "" ||
		filter.UsageAccountID != "" ||
		filter.ServiceCode != "" ||
		filter.ViewerRole != "" ||
		filter.ViewerAccountID != ""
	return filter
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

// billingVisibilityFilter resolves optional simulated-viewer controls into repository account constraints.
func (h billsHandler) billingVisibilityFilter(ctx context.Context, filter billsFilterView) (persistence.BillingVisibilityFilter, error) {
	resolution, err := h.billingPolicyFromFilter(ctx, filter, billingvisibility.ViewBills)
	if err != nil || !resolution.Scoped {
		return persistence.BillingVisibilityFilter{}, err
	}
	return billingVisibilityFilterFromPolicy(resolution.Policy), nil
}

// billingPolicyFromFilter builds the domain visibility policy from explicit viewer query fields.
func (h billsHandler) billingPolicyFromFilter(ctx context.Context, filter billsFilterView, requiredView billingvisibility.View) (viewerPolicyResolution, error) {
	return resolveViewerPolicy(ctx, h.db, exportViewerFieldsFromBillsFilter(filter), viewerPolicyResolveOptions{
		AllowUnscoped: true,
		RequiredView:  requiredView,
	})
}

// invoiceAccessPredicate separates bill-row visibility from financial document access.
func (h billsHandler) invoiceAccessPredicate(ctx context.Context, filter billsFilterView) (func(string) bool, error) {
	resolution, err := h.billingPolicyFromFilter(ctx, filter, "")
	if err != nil {
		return nil, err
	}
	if !resolution.Scoped {
		return func(string) bool { return true }, nil
	}
	policy := resolution.Policy
	return func(payerAccountID string) bool {
		return policy.AllowsView(billingvisibility.ViewInvoices) && policy.AllowsPayerAccount(strings.TrimSpace(payerAccountID))
	}, nil
}

// ensureInvoiceViewerAccess enforces financial-document access when a simulated viewer is selected.
func (h billsHandler) ensureInvoiceViewerAccess(ctx context.Context, r *http.Request, payerAccountID string) error {
	resolution, err := h.billingPolicyFromFilter(ctx, billsFilterFromRequest(r), billingvisibility.ViewInvoices)
	if err != nil || !resolution.Scoped {
		return err
	}
	policy := resolution.Policy
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
