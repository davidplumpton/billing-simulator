package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestMonthEndCloseFinalizesPeriodAndCreatesBill(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	clockRepo := NewSimulatorClockRepository(db)
	closeRepo := NewMonthEndCloseRepository(db)

	resource, err := usageRepo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-month-close-ec2",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  serviceAmazonEC2,
		ResourceType: "ec2_instance",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
		Tags: map[string]string{
			"app": "storefront",
		},
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-month-close-february",
		ResourceID:          resource.ID,
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: 2_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(February) error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-month-close-march",
		ResourceID:          resource.ID,
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-03-01T00:00:00Z",
		UsageEndTime:        "2026-03-01T01:00:00Z",
		UsageQuantityMicros: 1_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(March) error = %v", err)
	}
	if _, err := clockRepo.Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}

	result, err := closeRepo.ClosePreviousPeriod(ctx, MonthEndCloseRequest{
		PayerAccountID: "999988887777",
		InvoiceDueDays: 10,
	})
	if err != nil {
		t.Fatalf("ClosePreviousPeriod() error = %v", err)
	}
	if result.MeteringRecordsCreated != 1 || result.BillLineItemsCreated != 2 || result.FinalizedLineItems != 2 {
		t.Fatalf("ClosePreviousPeriod() = %+v, want one usage item plus Support finalized", result)
	}
	if result.Close.BillingPeriodStart != "2026-02-01" ||
		result.Close.BillingPeriodEnd != "2026-03-01" ||
		result.Close.PayerAccountID != "999988887777" ||
		result.Close.Status != billingPeriodCloseStatusClosed ||
		result.Close.FinalizedCostMicros != 1_083_200 ||
		result.Close.SummariesRefreshed != 2 ||
		result.Close.ClosedAt == "" {
		t.Fatalf("close = %+v, want February closed snapshot", result.Close)
	}
	if result.Bill.CloseID != result.Close.ID ||
		result.Bill.BillState != billStateIssued ||
		result.Bill.LineItemCount != 2 ||
		result.Bill.UsageChargeMicros != 1_083_200 ||
		result.Bill.TotalMicros != 1_083_200 ||
		result.Bill.CurrencyCode != "USD" ||
		result.Bill.IssuedAt == "" {
		t.Fatalf("bill = %+v, want issued bill reconciled to line item", result.Bill)
	}
	if result.InvoiceObligation.BillID != result.Bill.ID ||
		result.InvoiceObligation.Status != invoiceObligationStatusDue ||
		result.InvoiceObligation.AmountDueMicros != 1_083_200 ||
		result.InvoiceObligation.InvoiceDate != "2026-03-01" ||
		result.InvoiceObligation.DueDate != "2026-03-11" ||
		!strings.HasPrefix(result.InvoiceObligation.InvoiceID, "SIM-INV-202602-") {
		t.Fatalf("invoice obligation = %+v, want due invoice obligation", result.InvoiceObligation)
	}
	requireInvoiceDocumentMatchesBill(t, result.InvoiceDocument, result.Bill, result.InvoiceObligation)
	if len(result.Summaries) != 2 ||
		requireBillingPeriodSummary(t, result.Summaries, serviceAmazonEC2).LineItemStatus != billLineItemStatusFinal ||
		requireBillingPeriodSummary(t, result.Summaries, serviceAmazonEC2).UnblendedCostMicros != 83_200 ||
		requireBillingPeriodSummary(t, result.Summaries, serviceAWSSupport).UnblendedCostMicros != supportBusinessMinimumCostMicros {
		t.Fatalf("summaries = %+v, want final February service and Support summaries", result.Summaries)
	}

	items, err := NewBillLineItemRepository(db).ListBillLineItems(ctx, 10)
	if err != nil {
		t.Fatalf("ListBillLineItems() error = %v", err)
	}
	if len(items) != 2 ||
		requireBillLineItemByService(t, items, serviceAmazonEC2).LineItemStatus != billLineItemStatusFinal ||
		requireBillLineItemByService(t, items, serviceAWSSupport).LineItemStatus != billLineItemStatusFinal {
		t.Fatalf("bill line items = %+v, want finalized February usage and Support items", items)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bill_line_items SET description = description WHERE id = ?`, items[0].ID); err == nil {
		t.Fatal("updating a closed-period line item error = nil, want freeze trigger error")
	}

	replay, err := closeRepo.ClosePreviousPeriod(ctx, MonthEndCloseRequest{
		PayerAccountID: "999988887777",
		InvoiceDueDays: 10,
	})
	if err != nil {
		t.Fatalf("ClosePreviousPeriod(replay) error = %v", err)
	}
	if replay.Close.ID != result.Close.ID ||
		replay.Bill.ID != result.Bill.ID ||
		replay.InvoiceObligation.ID != result.InvoiceObligation.ID ||
		replay.InvoiceDocument.InvoiceID != result.InvoiceDocument.InvoiceID ||
		replay.MeteringRecordsCreated != 0 ||
		replay.BillLineItemsCreated != 0 {
		t.Fatalf("ClosePreviousPeriod(replay) = %+v, want existing close artifacts without new work", replay)
	}

	closes, err := closeRepo.ListRecentCloses(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentCloses() error = %v", err)
	}
	if len(closes) != 1 || closes[0].ID != result.Close.ID {
		t.Fatalf("ListRecentCloses() = %+v, want stored close", closes)
	}
	bills, err := closeRepo.ListIssuedBills(ctx, 10)
	if err != nil {
		t.Fatalf("ListIssuedBills() error = %v", err)
	}
	if len(bills) != 1 || bills[0].Bill.ID != result.Bill.ID || bills[0].Obligation.ID != result.InvoiceObligation.ID {
		t.Fatalf("ListIssuedBills() = %+v, want issued bill with obligation", bills)
	}
}

func TestMonthEndCloseRejectsOpenPeriod(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	closeRepo := NewMonthEndCloseRepository(db)

	_, err := closeRepo.ClosePreviousPeriod(ctx, MonthEndCloseRequest{
		PayerAccountID: "111122223333",
		PeriodStart:    "2026-02-01",
		PeriodEnd:      "2026-03-01",
	})
	if err == nil {
		t.Fatal("ClosePreviousPeriod(open period) error = nil, want not-ended error")
	}
	if !strings.Contains(err.Error(), "has not ended") {
		t.Fatalf("ClosePreviousPeriod(open period) error = %q, want not-ended message", err.Error())
	}
}

func TestMonthEndCloseRejectsCrossPeriodLineItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	clockRepo := NewSimulatorClockRepository(db)
	closeRepo := NewMonthEndCloseRepository(db)

	item := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-month-close-cross-period",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-month-close-cross-period",
			ResourceID:          "resource-month-close-cross-period",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-28T22:00:00Z",
			UsageEndTime:        "2026-03-01T00:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)
	// Simulate a legacy cross-period row that predates the period-bounds trigger.
	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_cross_period_bill_line_item_update`); err != nil {
		t.Fatalf("DROP TRIGGER reject_cross_period_bill_line_item_update error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bill_line_items SET usage_end_time = ? WHERE id = ?`, "2026-03-01T02:00:00Z", item.ID); err != nil {
		t.Fatalf("make legacy cross-period bill line item: %v", err)
	}
	if _, err := clockRepo.Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}

	_, err := closeRepo.ClosePreviousPeriod(ctx, MonthEndCloseRequest{
		PayerAccountID: item.PayerAccountID,
	})
	if err == nil {
		t.Fatal("ClosePreviousPeriod(cross-period item) error = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "crosses billing period") {
		t.Fatalf("ClosePreviousPeriod(cross-period item) error = %q, want cross-period message", err.Error())
	}
}

func TestMonthEndCloseRejectsPayerMismatchedPrepricedLineItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	clockRepo := NewSimulatorClockRepository(db)
	jobRepo := NewDailyMeteringJobRepository(db)
	closeRepo := NewMonthEndCloseRepository(db)

	resource, err := usageRepo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-month-close-payer-mismatch",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  serviceAmazonEC2,
		ResourceType: "ec2_instance",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-month-close-payer-mismatch",
		ResourceID:          resource.ID,
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: 2_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if _, err := clockRepo.Set(ctx, "2026-02-02T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock daily) error = %v", err)
	}
	daily, err := jobRepo.Run(ctx, DailyMeteringJobRequest{
		Trigger:        DailyMeteringJobTriggerOnDemand,
		PayerAccountID: "111122223333",
	})
	if err != nil {
		t.Fatalf("Run(daily metering) error = %v", err)
	}
	if daily.MeteringRecordsCreated != 1 || daily.BillLineItemsCreated != 2 {
		t.Fatalf("Run(daily metering) = %+v, want one metering record plus usage and Support items", daily)
	}
	items, err := NewBillLineItemRepository(db).ListBillLineItems(ctx, 10)
	if err != nil {
		t.Fatalf("ListBillLineItems() error = %v", err)
	}
	usageItem := requireBillLineItemByService(t, items, serviceAmazonEC2)
	if usageItem.PayerAccountID != "111122223333" || usageItem.UsageAccountID != "111122223333" {
		t.Fatalf("usage bill line item accounts = payer %s usage %s, want member-payer prepriced item", usageItem.PayerAccountID, usageItem.UsageAccountID)
	}
	if _, err := clockRepo.Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock close) error = %v", err)
	}

	_, err = closeRepo.ClosePreviousPeriod(ctx, MonthEndCloseRequest{
		PayerAccountID: "999988887777",
		InvoiceDueDays: 10,
	})
	if err == nil {
		t.Fatal("ClosePreviousPeriod(payer mismatch) error = nil, want rejection")
	}
	for _, want := range []string{"payer-mismatched", usageItem.ID, "111122223333", "999988887777", "2026-02-01 to 2026-03-01"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ClosePreviousPeriod(payer mismatch) error = %q, want %q", err.Error(), want)
		}
	}
	var closeCount, billCount, finalLineItemCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_period_closes`).Scan(&closeCount); err != nil {
		t.Fatalf("count billing_period_closes: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bills`).Scan(&billCount); err != nil {
		t.Fatalf("count bills: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bill_line_items WHERE line_item_status = ?`, billLineItemStatusFinal).Scan(&finalLineItemCount); err != nil {
		t.Fatalf("count final bill_line_items: %v", err)
	}
	if closeCount != 0 || billCount != 0 || finalLineItemCount != 0 {
		t.Fatalf("close side effects = closes %d bills %d final line items %d, want none", closeCount, billCount, finalLineItemCount)
	}
}

func TestMonthEndCloseRejectsNilDB(t *testing.T) {
	t.Parallel()

	repo := NewMonthEndCloseRepository(nil)
	if _, err := repo.ClosePreviousPeriod(context.Background(), MonthEndCloseRequest{PayerAccountID: "111122223333"}); err == nil {
		t.Fatal("ClosePreviousPeriod(nil DB) error = nil, want database handle validation error")
	}
	if _, err := repo.ListRecentCloses(context.Background(), 10); err == nil {
		t.Fatal("ListRecentCloses(nil DB) error = nil, want database handle validation error")
	}
	if _, err := repo.ListIssuedBills(context.Background(), 10); err == nil {
		t.Fatal("ListIssuedBills(nil DB) error = nil, want database handle validation error")
	}
}
