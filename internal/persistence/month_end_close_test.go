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
	if result.MeteringRecordsCreated != 1 || result.BillLineItemsCreated != 1 || result.FinalizedLineItems != 1 {
		t.Fatalf("ClosePreviousPeriod() = %+v, want one metered, priced, and finalized item", result)
	}
	if result.Close.BillingPeriodStart != "2026-02-01" ||
		result.Close.BillingPeriodEnd != "2026-03-01" ||
		result.Close.PayerAccountID != "999988887777" ||
		result.Close.Status != billingPeriodCloseStatusClosed ||
		result.Close.FinalizedCostMicros != 83_200 ||
		result.Close.SummariesRefreshed != 1 ||
		result.Close.ClosedAt == "" {
		t.Fatalf("close = %+v, want February closed snapshot", result.Close)
	}
	if result.Bill.CloseID != result.Close.ID ||
		result.Bill.BillState != billStateIssued ||
		result.Bill.LineItemCount != 1 ||
		result.Bill.UsageChargeMicros != 83_200 ||
		result.Bill.TotalMicros != 83_200 ||
		result.Bill.CurrencyCode != "USD" ||
		result.Bill.IssuedAt == "" {
		t.Fatalf("bill = %+v, want issued bill reconciled to line item", result.Bill)
	}
	if result.InvoiceObligation.BillID != result.Bill.ID ||
		result.InvoiceObligation.Status != invoiceObligationStatusDue ||
		result.InvoiceObligation.AmountDueMicros != 83_200 ||
		result.InvoiceObligation.InvoiceDate != "2026-03-01" ||
		result.InvoiceObligation.DueDate != "2026-03-11" ||
		!strings.HasPrefix(result.InvoiceObligation.InvoiceID, "SIM-INV-202602-") {
		t.Fatalf("invoice obligation = %+v, want due invoice obligation", result.InvoiceObligation)
	}
	if len(result.Summaries) != 1 ||
		result.Summaries[0].LineItemStatus != billLineItemStatusFinal ||
		result.Summaries[0].UnblendedCostMicros != 83_200 {
		t.Fatalf("summaries = %+v, want final February service summary", result.Summaries)
	}

	items, err := NewBillLineItemRepository(db).ListBillLineItems(ctx, 10)
	if err != nil {
		t.Fatalf("ListBillLineItems() error = %v", err)
	}
	if len(items) != 1 || items[0].LineItemStatus != billLineItemStatusFinal {
		t.Fatalf("bill line items = %+v, want only finalized February item", items)
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

	recordAndPriceSingleUsage(t, ctx, db,
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
			UsageEndTime:        "2026-03-01T02:00:00Z",
			UsageQuantityMicros: 4_000_000,
			UsageUnit:           "Hours",
		},
	)
	if _, err := clockRepo.Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}

	_, err := closeRepo.ClosePreviousPeriod(ctx, MonthEndCloseRequest{
		PayerAccountID: "111122223333",
	})
	if err == nil {
		t.Fatal("ClosePreviousPeriod(cross-period item) error = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "crosses billing period") {
		t.Fatalf("ClosePreviousPeriod(cross-period item) error = %q, want cross-period message", err.Error())
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
