package app

import (
	"net/http"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

func exportReconciliationFilterFromRequest(r *http.Request) exportReconciliationFilterView {
	query := r.URL.Query()
	filter := exportReconciliationFilterView{
		BillingPeriodStart: query.Get("billing_period_start"),
		BillingPeriodEnd:   query.Get("billing_period_end"),
		PayerAccountID:     query.Get("payer_account_id"),
		UsageAccountID:     query.Get("usage_account_id"),
		ViewerRole:         query.Get("viewer_role"),
		ViewerAccountID:    query.Get("viewer_account_id"),
		LineItemStatus:     query.Get("line_item_status"),
		Limit:              query.Get("limit"),
		ApplyButton:        uiSubmitButton("Run Report"),
		ClearPath:          "/exports/reconciliation",
	}
	filter.ViewerRoleField = viewerRoleSelectField(filter.ViewerRole, "Default viewer")
	filter.ViewerAccountField = viewerAccountIDField(filter.ViewerAccountID)
	filter.LineItemStatusField = exportReconciliationLineItemStatusSelect(filter.LineItemStatus)
	filter.HasFilters = filter.BillingPeriodStart != "" ||
		filter.BillingPeriodEnd != "" ||
		filter.PayerAccountID != "" ||
		filter.UsageAccountID != "" ||
		filter.ViewerRole != "" ||
		filter.ViewerAccountID != "" ||
		filter.LineItemStatus != "" ||
		filter.Limit != ""
	return filter
}

func exportReconciliationLineItemStatusSelect(selected string) uiSelectFieldView {
	options := []uiSelectOptionView{
		{Value: "", Label: "All statuses"},
		{Value: "final", Label: "Final"},
		{Value: "estimated", Label: "Estimated"},
	}
	for idx := range options {
		options[idx].Selected = options[idx].Value == selected
	}
	return uiSelectFieldView{
		Label:   "Line Item Status",
		Name:    "line_item_status",
		Options: options,
	}
}

func exportReconciliationReportViewFromReport(report persistence.CURExportReconciliationReport, viewer exportViewerFields) exportReconciliationReportView {
	usageAccountID := strings.TrimSpace(report.UsageAccountID)
	if usageAccountID == "" {
		usageAccountID = "all accounts"
	}
	lineItemStatus := strings.TrimSpace(report.LineItemStatus)
	if lineItemStatus == "" {
		lineItemStatus = "all statuses"
	}
	view := exportReconciliationReportView{
		Period:         report.BillingPeriodStart + " to " + report.BillingPeriodEnd,
		PayerAccountID: report.PayerAccountID,
		UsageAccountID: usageAccountID,
		LineItemStatus: lineItemStatus,
		CurrencyCode:   report.CurrencyCode,
		Status:         displayBillState(report.Status),
		Flags:          strings.Join(report.Flags, ", "),
		CURCSVPath: curCSVExportPathWithViewer(persistence.CURCSVExportRequest{
			BillingPeriodStart: report.BillingPeriodStart,
			BillingPeriodEnd:   report.BillingPeriodEnd,
			PayerAccountID:     report.PayerAccountID,
			UsageAccountID:     report.UsageAccountID,
			LineItemStatus:     report.LineItemStatus,
			Limit:              report.Limit,
		}, viewer),
	}
	view.DocumentRows = []exportReconciliationDocumentRowView{
		{
			Source:         "Export",
			ID:             "CUR-like CSV",
			Status:         lineItemStatus,
			LineItemCount:  report.ExportLineItemCount,
			Charges:        formatUSDMicros(report.ExportChargeMicros),
			Credits:        formatUSDMicros(report.ExportCreditMicros),
			Refunds:        formatUSDMicros(report.ExportRefundMicros),
			Tax:            formatUSDMicros(report.ExportTaxMicros),
			Total:          formatUSDMicros(report.ExportTotalMicros),
			ItemResidual:   "0",
			ChargeResidual: formatUSDMicros(0),
			CreditResidual: formatUSDMicros(0),
			RefundResidual: formatUSDMicros(0),
			TaxResidual:    formatUSDMicros(0),
			TotalResidual:  formatUSDMicros(0),
		},
		{
			Source:         "Bill",
			ID:             displayOptionalValue(report.BillID),
			Status:         displayBillState(report.BillState),
			LineItemCount:  report.BillLineItemCount,
			Charges:        formatUSDMicros(report.BillChargeMicros),
			Credits:        formatUSDMicros(report.BillCreditMicros),
			Refunds:        formatUSDMicros(report.BillRefundMicros),
			Tax:            formatUSDMicros(report.BillTaxMicros),
			Total:          formatUSDMicros(report.BillTotalMicros),
			ItemResidual:   strconv.Itoa(report.BillLineItemResidual),
			ChargeResidual: formatUSDMicros(report.BillChargeResidualMicros),
			CreditResidual: formatUSDMicros(report.BillCreditResidualMicros),
			RefundResidual: formatUSDMicros(report.BillRefundResidualMicros),
			TaxResidual:    formatUSDMicros(report.BillTaxResidualMicros),
			TotalResidual:  formatUSDMicros(report.BillTotalResidualMicros),
		},
		{
			Source:         "Invoice",
			ID:             displayOptionalValue(report.InvoiceID),
			Status:         displayBillState(report.InvoiceStatus),
			LineItemCount:  report.InvoiceLineItemCount,
			Charges:        formatUSDMicros(report.InvoiceChargeMicros),
			Credits:        formatUSDMicros(report.InvoiceCreditMicros),
			Refunds:        formatUSDMicros(report.InvoiceRefundMicros),
			Tax:            formatUSDMicros(report.InvoiceTaxMicros),
			Total:          formatUSDMicros(report.InvoiceTotalMicros),
			ItemResidual:   strconv.Itoa(report.InvoiceLineItemResidual),
			ChargeResidual: formatUSDMicros(report.InvoiceChargeResidualMicros),
			CreditResidual: formatUSDMicros(report.InvoiceCreditResidualMicros),
			RefundResidual: formatUSDMicros(report.InvoiceRefundResidualMicros),
			TaxResidual:    formatUSDMicros(report.InvoiceTaxResidualMicros),
			TotalResidual:  formatUSDMicros(report.InvoiceTotalResidualMicros),
		},
	}
	return view
}
