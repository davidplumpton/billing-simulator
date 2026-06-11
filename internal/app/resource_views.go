package app

import (
	"context"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func resourceViewFromSummary(summary persistence.ResourceSummary) resourceView {
	resource := summary.Resource
	name := displayResourceName(resource)
	return resourceView{
		ID:               resource.ID,
		Name:             name,
		AccountID:        resource.AccountID,
		RegionCode:       resource.RegionCode,
		ServiceCode:      resource.ServiceCode,
		ResourceType:     resource.ResourceType,
		Size:             resource.Attributes["size"],
		Status:           resource.Status,
		CreatedAt:        resource.CreatedAt,
		UsageEventCount:  summary.UsageEventCount,
		LastUsageEndTime: summary.LastUsageEndTime,
		Tags:             keyValueViews(summary.ActiveTags),
		Attributes:       keyValueViews(resource.Attributes),
	}
}

func meteringRecordViewFromRecord(record persistence.MeteringRecord, resourceName string) meteringRecordView {
	if resourceName == "" {
		resourceName = record.ResourceID
	}
	return meteringRecordView{
		ResourceName:       resourceName,
		AccountID:          record.AccountID,
		ServiceCode:        record.ServiceCode,
		BillableDimensions: billableDimensions(record.ServiceCode, record.UsageType, record.Operation, record.RegionCode),
		Window:             record.UsageStartTime + " to " + record.UsageEndTime,
		Quantity:           formatQuantityMicros(record.UsageQuantityMicros),
		Unit:               record.UsageUnit,
		Tags:               keyValueViews(record.TagSnapshot),
	}
}

func billLineItemViewFromItem(item persistence.BillLineItem, resourceName string) billLineItemView {
	if resourceName == "" {
		resourceName = item.ResourceID
	}
	if resourceName == "" {
		resourceName = item.ServiceName
	}
	return billLineItemView{
		ResourceName:     resourceName,
		Period:           item.BillingPeriodStart + " to " + item.BillingPeriodEnd,
		Status:           item.LineItemStatus,
		PayerAccountID:   item.PayerAccountID,
		UsageAccountID:   item.UsageAccountID,
		ServiceCode:      item.ServiceCode,
		Description:      item.Description,
		PricingQuantity:  formatQuantityMicros(item.PricingQuantityMicros),
		PricingUnit:      item.PricingUnit,
		UnblendedRate:    formatUSDMicros(item.UnblendedRateMicros),
		UnblendedCost:    formatUSDMicros(item.UnblendedCostMicros),
		PriceCatalogSKU:  item.PriceCatalogSKU,
		PriceEffectiveOn: item.PriceEffectiveDate,
		Tags:             keyValueViews(item.TagSnapshot),
	}
}

func billingPeriodSummaryViewFromSummary(summary persistence.BillingPeriodServiceSummary) billingPeriodSummaryView {
	return billingPeriodSummaryView{
		Period:         summary.BillingPeriodStart + " to " + summary.BillingPeriodEnd,
		PayerAccountID: summary.PayerAccountID,
		UsageAccountID: summary.UsageAccountID,
		ServiceCode:    summary.ServiceCode,
		Status:         summary.LineItemStatus,
		LineItemCount:  summary.LineItemCount,
		Cost:           formatUSDMicros(summary.UnblendedCostMicros),
		RefreshedAt:    summary.RefreshedAt,
	}
}

func dailyMeteringJobRunViewFromRun(run persistence.DailyMeteringJobRun) dailyMeteringJobRunView {
	return dailyMeteringJobRunView{
		ID:                     run.ID,
		Trigger:                string(run.Trigger),
		ClockTime:              run.ClockTime,
		PayerAccountID:         run.PayerAccountID,
		MeteringRecordsCreated: run.MeteringRecordsCreated,
		BillLineItemsCreated:   run.BillLineItemsCreated,
		SummariesRefreshed:     run.SummariesRefreshed,
		CompletedAt:            run.CompletedAt,
	}
}

func monthEndCloseViewFromClose(close persistence.BillingPeriodClose) monthEndCloseView {
	return monthEndCloseView{
		ID:                     close.ID,
		Period:                 close.BillingPeriodStart + " to " + close.BillingPeriodEnd,
		PayerAccountID:         close.PayerAccountID,
		Status:                 close.Status,
		MeteringRecordsCreated: close.MeteringRecordsCreated,
		BillLineItemsCreated:   close.BillLineItemsCreated,
		FinalizedLineItems:     close.FinalizedLineItemCount,
		FinalizedCost:          formatUSDMicros(close.FinalizedCostMicros),
		SummariesRefreshed:     close.SummariesRefreshed,
		ClosedAt:               close.ClosedAt,
	}
}

func issuedBillViewFromBill(issued persistence.BillWithInvoiceObligation) issuedBillView {
	return issuedBillView{
		ID:               issued.Bill.ID,
		Period:           issued.Bill.BillingPeriodStart + " to " + issued.Bill.BillingPeriodEnd,
		PayerAccountID:   issued.Bill.PayerAccountID,
		BillState:        issued.Bill.BillState,
		LineItemCount:    issued.Bill.LineItemCount,
		UsageCharge:      formatUSDMicros(issued.Bill.UsageChargeMicros),
		Credits:          formatUSDMicros(issued.Bill.CreditMicros),
		Refunds:          formatUSDMicros(issued.Bill.RefundMicros),
		Tax:              formatUSDMicros(issued.Bill.TaxMicros),
		Total:            formatUSDMicros(issued.Bill.TotalMicros),
		InvoiceID:        issued.Obligation.InvoiceID,
		InvoiceStatus:    issued.Obligation.Status,
		InvoiceAmountDue: formatUSDMicros(issued.Obligation.AmountDueMicros),
		InvoiceDate:      issued.Obligation.InvoiceDate,
		InvoiceDueDate:   issued.Obligation.DueDate,
	}
}

func (h resourceLabHandler) usageEventView(ctx context.Context, event persistence.UsageEvent, resourceName string) usageEventView {
	if resourceName == "" {
		resourceName = event.ResourceID
	}
	costEstimate := "unpriced"
	usageDate, billingPeriodDays, ok := usageEstimatePeriod(event.UsageStartTime)
	if ok {
		lookupResult, err := h.catalog.Lookup(ctx, persistence.PriceLookupRequest{
			ServiceCode:         event.ServiceCode,
			UsageType:           event.UsageType,
			Operation:           event.Operation,
			RegionCode:          event.RegionCode,
			UsageUnit:           event.UsageUnit,
			UsageQuantityMicros: event.UsageQuantityMicros,
			UsageDate:           usageDate,
			BillingPeriodDays:   billingPeriodDays,
		})
		if err == nil {
			costEstimate = formatUSDMicros(lookupResult.CostMicros)
		}
	}

	return usageEventView{
		ID:                 event.ID,
		ResourceID:         event.ResourceID,
		ResourceName:       resourceName,
		AccountID:          event.AccountID,
		ServiceCode:        event.ServiceCode,
		UsageType:          event.UsageType,
		Operation:          event.Operation,
		RegionCode:         event.RegionCode,
		Window:             event.UsageStartTime + " to " + event.UsageEndTime,
		Quantity:           formatQuantityMicros(event.UsageQuantityMicros),
		Unit:               event.UsageUnit,
		EstimatedCost:      costEstimate,
		BillableDimensions: billableDimensions(event.ServiceCode, event.UsageType, event.Operation, event.RegionCode),
		Tags:               keyValueViews(event.TagSnapshot),
	}
}

func catalogItemViewFromCatalog(item persistence.PriceCatalogItem, billingPeriodDays int) catalogItemView {
	periodEstimate := ""
	if strings.Contains(strings.ToLower(item.Unit), "hour") {
		periodEstimate = "24h " + formatUSDMicros(item.RateMicros*24)
	}
	if strings.EqualFold(item.Unit, "GBMonth") && billingPeriodDays > 0 {
		periodEstimate = "100 GB-day " + formatUSDMicros(divideAndRoundInt64(item.RateMicros*100, int64(billingPeriodDays)))
	}
	return catalogItemView{
		ServiceCode:        item.ServiceCode,
		UsageType:          item.UsageType,
		Operation:          item.Operation,
		RegionCode:         item.RegionCode,
		BillableDimensions: billableDimensions(item.ServiceCode, item.UsageType, item.Operation, item.RegionCode),
		Unit:               item.Unit,
		UnitRate:           formatUSDMicros(item.RateMicros),
		PeriodEstimate:     periodEstimate,
	}
}

func displayResourceName(resource persistence.Resource) string {
	if strings.TrimSpace(resource.ResourceName) != "" {
		return resource.ResourceName
	}
	return resource.ID
}

// usageEstimatePeriod returns the lookup date and calendar-month days used for UI-only price estimates.
func usageEstimatePeriod(value string) (string, int, bool) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return "", 0, false
	}
	period, err := persistence.BillingPeriodForTime(parsed)
	if err != nil {
		return "", 0, false
	}
	return parsed.UTC().Format(time.DateOnly), period.Days, true
}

func billableDimensions(serviceCode, usageType, operation, regionCode string) string {
	return serviceCode + " / " + usageType + " / " + operation + " / " + regionCode
}

func divideAndRoundInt64(value, divisor int64) int64 {
	quotient := value / divisor
	remainder := value % divisor
	if remainder*2 >= divisor {
		return quotient + 1
	}
	return quotient
}
