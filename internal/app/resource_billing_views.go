package app

import (
	"strings"

	"aws-billing-simulator/internal/persistence"
)

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

func divideAndRoundInt64(value, divisor int64) int64 {
	quotient := value / divisor
	remainder := value % divisor
	if remainder*2 >= divisor {
		return quotient + 1
	}
	return quotient
}
