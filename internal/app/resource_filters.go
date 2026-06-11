package app

import (
	"net/http"
	"strings"
)

// resourceFilterFromRequest normalizes the GET filters used by links and partial refreshes.
func resourceFilterFromRequest(r *http.Request) resourceFilterView {
	query := r.URL.Query()
	filter := resourceFilterView{
		AccountID:   strings.TrimSpace(query.Get("account_id")),
		ServiceCode: strings.TrimSpace(query.Get("service_code")),
		ApplyButton: uiSubmitButton("Apply Filters"),
		ClearPath:   "/resources",
	}
	filter.HasFilters = filter.AccountID != "" || filter.ServiceCode != ""
	return filter
}

// applyResourceFilters limits all resource-lab tables to rows matching the active GET filters.
func applyResourceFilters(data *resourcePageData) {
	filter := data.Filters
	if !filter.HasFilters {
		return
	}
	data.Resources = filterResourceViews(data.Resources, filter)
	data.UsageEvents = filterUsageEventViews(data.UsageEvents, filter)
	data.MeteringRecords = filterMeteringRecordViews(data.MeteringRecords, filter)
	data.BillLineItems = filterResourceBillLineItemViews(data.BillLineItems, filter)
	data.BillingPeriodSummaries = filterBillingPeriodSummaryViews(data.BillingPeriodSummaries, filter)
	data.DailyMeteringJobRuns = filterDailyMeteringJobRunViews(data.DailyMeteringJobRuns, filter)
	data.MonthEndCloses = filterMonthEndCloseViews(data.MonthEndCloses, filter)
	data.IssuedBills = filterIssuedBillViews(data.IssuedBills, filter)
	data.CatalogItems = filterCatalogItemViews(data.CatalogItems, filter)
}

func filterResourceViews(rows []resourceView, filter resourceFilterView) []resourceView {
	filtered := make([]resourceView, 0, len(rows))
	for _, row := range rows {
		if matchesFilter(row.AccountID, filter.AccountID) &&
			matchesFilter(row.ServiceCode, filter.ServiceCode) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterUsageEventViews(rows []usageEventView, filter resourceFilterView) []usageEventView {
	filtered := make([]usageEventView, 0, len(rows))
	for _, row := range rows {
		if matchesFilter(row.AccountID, filter.AccountID) && matchesFilter(row.ServiceCode, filter.ServiceCode) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterMeteringRecordViews(rows []meteringRecordView, filter resourceFilterView) []meteringRecordView {
	filtered := make([]meteringRecordView, 0, len(rows))
	for _, row := range rows {
		if matchesFilter(row.AccountID, filter.AccountID) && matchesFilter(row.ServiceCode, filter.ServiceCode) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterResourceBillLineItemViews(rows []billLineItemView, filter resourceFilterView) []billLineItemView {
	filtered := make([]billLineItemView, 0, len(rows))
	for _, row := range rows {
		if (matchesFilter(row.UsageAccountID, filter.AccountID) || matchesFilter(row.PayerAccountID, filter.AccountID)) &&
			matchesFilter(row.ServiceCode, filter.ServiceCode) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterBillingPeriodSummaryViews(rows []billingPeriodSummaryView, filter resourceFilterView) []billingPeriodSummaryView {
	filtered := make([]billingPeriodSummaryView, 0, len(rows))
	for _, row := range rows {
		if (matchesFilter(row.UsageAccountID, filter.AccountID) || matchesFilter(row.PayerAccountID, filter.AccountID)) &&
			matchesFilter(row.ServiceCode, filter.ServiceCode) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterDailyMeteringJobRunViews(rows []dailyMeteringJobRunView, filter resourceFilterView) []dailyMeteringJobRunView {
	filtered := make([]dailyMeteringJobRunView, 0, len(rows))
	for _, row := range rows {
		if matchesFilter(row.PayerAccountID, filter.AccountID) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterMonthEndCloseViews(rows []monthEndCloseView, filter resourceFilterView) []monthEndCloseView {
	filtered := make([]monthEndCloseView, 0, len(rows))
	for _, row := range rows {
		if matchesFilter(row.PayerAccountID, filter.AccountID) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterIssuedBillViews(rows []issuedBillView, filter resourceFilterView) []issuedBillView {
	filtered := make([]issuedBillView, 0, len(rows))
	for _, row := range rows {
		if matchesFilter(row.PayerAccountID, filter.AccountID) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterCatalogItemViews(rows []catalogItemView, filter resourceFilterView) []catalogItemView {
	filtered := make([]catalogItemView, 0, len(rows))
	for _, row := range rows {
		if matchesFilter(row.ServiceCode, filter.ServiceCode) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func matchesFilter(value, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(value), filter)
}

// resourceTables defines the shared dense-table metadata for the resource lab page.
func resourceTables() resourceTablesView {
	return resourceTablesView{
		Inventory:              uiTable(uiTableHeaders("Name", "Account", "Service", "Region", "Size", "Status", "Tags", "Usage"), "No resources"),
		RecentUsage:            uiTable(uiTableHeaders("Resource", "Billable Dimensions", "Window", "Quantity", "Estimated Cost", "Tags Snapshot"), "No usage events"),
		BillingPeriodSummaries: uiTable(uiTableHeaders("Period", "Payer", "Usage Account", "Service", "Status", "Items", "Cost", "Refreshed"), "No billing summary"),
		DailyMeteringJobRuns:   uiTable(uiTableHeaders("Completed", "Trigger", "Clock", "Payer", "Metering", "Line Items", "Summaries"), "No daily metering jobs"),
		MonthEndCloses:         uiTable(uiTableHeaders("Closed", "Period", "Payer", "Status", "Metering", "Line Items", "Final Cost", "Summaries"), "No closed billing periods"),
		IssuedBills:            uiTable(uiTableHeaders("Bill", "Period", "Payer", "State", "Items", "Charges", "Tax", "Total", "Invoice", "Due"), "No issued bills"),
		MeteringRecords:        uiTable(uiTableHeaders("Resource", "Billable Dimensions", "Window", "Quantity", "Tags Snapshot"), "No metering records"),
		BillLineItems:          uiTable(uiTableHeaders("Resource", "Period", "Status", "Accounts", "Service", "Description", "Usage", "Rate", "Cost", "Tags Snapshot"), "No bill line items"),
		PriceDimensions:        uiTable(uiTableHeaders("Service", "Billable Dimensions", "Unit", "Rate", "Estimate"), "No price dimensions"),
	}
}

// clockAdvanceUnitSelectField prepares simulator-clock units for the shared select partial.
func clockAdvanceUnitSelectField(units []clockAdvanceUnitView) uiSelectFieldView {
	options := make([]uiSelectOptionView, 0, len(units))
	for _, unit := range units {
		options = append(options, uiSelectOptionView{Value: string(unit.Key), Label: unit.Label})
	}
	return uiSelectFieldView{
		Label:    "Unit",
		Name:     "clock_advance_unit",
		Options:  options,
		Required: true,
	}
}
